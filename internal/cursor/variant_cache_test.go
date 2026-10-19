package cursor

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
)

func TestCachedAccountDoesNotWaitForOtherDiscovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		blocked := make(chan struct{})
		cold := testRequest()
		cold.StorageJSON = []byte(`{"type":"cursor","id":"cold","access_token":"cold"}`)
		cold.HTTPClient = fixtureHost{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
			<-blocked
			return pluginapi.HTTPResponse{StatusCode: http.StatusOK}, nil
		}}
		go func() { _, _, _ = e.variantTableFor(context.Background(), cold) }()
		synctest.Wait()
		finished := false
		go func() {
			_, _, err := e.variantTableFor(context.Background(), testRequest())
			if err != nil {
				t.Error(err)
			}
			finished = true
		}()
		synctest.Wait()
		if !finished {
			t.Error("cached account waited for another account's network request")
		}
		close(blocked)
		synctest.Wait()
	})
}

func TestConcurrentVariantMissesShareFetchAndWaitersCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := NewExecutor(DefaultConfig())
		defer e.Close()
		blocked := make(chan struct{})
		var calls atomic.Int32
		req := testRequest()
		req.HTTPClient = fixtureHost{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
			calls.Add(1)
			<-blocked
			raw, _ := proto.Marshal(&pb.GetUsableModelsResponse{Models: variantRows("fixture")})
			return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: raw}, nil
		}}
		results := make(chan *variantTable, 2)
		go func() {
			table, _, err := e.variantTableFor(context.Background(), req)
			if err != nil {
				t.Error(err)
			}
			results <- table
		}()
		synctest.Wait()
		go func() {
			table, _, err := e.variantTableFor(context.Background(), req)
			if err != nil {
				t.Error(err)
			}
			results <- table
		}()
		ctx, cancel := context.WithCancel(context.Background())
		cancelled := make(chan error, 1)
		go func() {
			_, _, err := e.variantTableFor(ctx, req)
			cancelled <- err
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case err := <-cancelled:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("cancelled waiter returned %v", err)
			}
		default:
			t.Error("cancelled waiter remained blocked")
		}
		close(blocked)
		synctest.Wait()
		first, second := <-results, <-results
		if calls.Load() != 1 || first != second || first == nil {
			t.Errorf("calls=%d first=%p second=%p", calls.Load(), first, second)
		}
	})
}

func TestVariantFetchFailureCanRetryAndExpiredEntriesAreRemoved(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	e.variants = map[string]variantCacheEntry{"expired": {expires: time.Now().Add(-time.Second)}}
	req := testRequest()
	calls := 0
	req.HTTPClient = fixtureHost{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		calls++
		if calls == 1 {
			return pluginapi.HTTPResponse{StatusCode: 503}, nil
		}
		raw, _ := proto.Marshal(&pb.GetUsableModelsResponse{Models: variantRows("fixture")})
		return pluginapi.HTTPResponse{StatusCode: 200, Body: raw}, nil
	}}
	if _, _, err := e.variantTableFor(context.Background(), req); err == nil {
		t.Fatal("upstream failure accepted")
	}
	if table, _, err := e.variantTableFor(context.Background(), req); err != nil || table.byBase["fixture"] == nil {
		t.Fatalf("retry failed: table=%v error=%v", table, err)
	}
	if _, ok := e.variants["expired"]; ok {
		t.Fatal("expired credential catalogue retained")
	}
}

func TestSharedDiscoverySurvivesOwnerCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			e := NewExecutor(DefaultConfig())
			defer e.Close()
			var calls atomic.Int32
			req := testRequest()
			req.HTTPClient = fixtureHost{do: func(ctx context.Context, _ pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				if calls.Add(1) == 1 {
					<-ctx.Done()
					return pluginapi.HTTPResponse{}, ctx.Err()
				}
				raw, _ := proto.Marshal(&pb.GetUsableModelsResponse{Models: variantRows("fixture")})
				return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: raw}, nil
			}}
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			owner := make(chan error, 1)
			go func() {
				_, _, err := e.variantTableFor(ctx, req)
				owner <- err
			}()
			synctest.Wait()
			type result struct {
				table *variantTable
				err   error
			}
			waiters := make(chan result, 2)
			for range 2 {
				go func() {
					table, _, err := e.variantTableFor(context.Background(), req)
					waiters <- result{table: table, err: err}
				}()
			}
			synctest.Wait()
			want := context.Canceled
			if deadline {
				want = context.DeadlineExceeded
				time.Sleep(time.Second)
			} else {
				cancel()
			}
			synctest.Wait()
			if err := <-owner; !errors.Is(err, want) {
				t.Errorf("owner error=%v want=%v", err, want)
			}
			first, second := <-waiters, <-waiters
			if first.err != nil || second.err != nil || first.table == nil || first.table != second.table || first.table.byBase["fixture"] == nil || calls.Load() != 2 {
				t.Fatalf("live waiters lost discovery: first=%+v second=%+v calls=%d", first, second, calls.Load())
			}
		})
	}
}

func TestSharedDiscoveryTimeoutDoesNotRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := NewExecutor(Config{Enabled: true, RequestTimeout: 1})
		defer e.Close()
		var calls atomic.Int32
		req := testRequest()
		req.HTTPClient = fixtureHost{do: func(ctx context.Context, _ pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
			calls.Add(1)
			<-ctx.Done()
			return pluginapi.HTTPResponse{}, ctx.Err()
		}}
		results := make(chan error, 2)
		for range 2 {
			go func() {
				_, _, err := e.variantTableFor(context.Background(), req)
				results <- err
			}()
			synctest.Wait()
		}
		for range 2 {
			if err := <-results; !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("discovery deadline changed: %v", err)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("shared discovery timeout retried %d times", calls.Load())
		}
	})
}
