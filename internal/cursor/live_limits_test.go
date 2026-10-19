package cursor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestLiveToolLoopRejectsAdmissionWithoutEvictingPendingRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		var scopes []*liveFixtureScope
		for index := 0; index < 24; index++ {
			scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
			scopes = append(scopes, scope)
			req := liveRequest(&liveFixtureHost{scope: scope})
			req.Headers.Set("X-Opencode-Session-Id", fmt.Sprintf("session-%d", index))
			if _, err := e.Execute(context.Background(), req); err != nil {
				t.Fatal(err)
			}
		}
		req := liveRequest(&liveFixtureHost{scope: &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}})
		req.Headers.Set("X-Opencode-Session-Id", "over-capacity")
		if _, err := e.Execute(context.Background(), req); err == nil || !strings.Contains(err.Error(), "capacity") {
			t.Fatalf("excess admission=%v", err)
		}
		e.liveMu.Lock()
		count := len(e.live)
		e.liveMu.Unlock()
		if count != 24 {
			t.Fatalf("pending runs=%d", count)
		}
		for _, scope := range scopes {
			scope.mu.Lock()
			closed := scope.closed
			scope.mu.Unlock()
			if closed {
				t.Fatal("capacity pressure evicted pending work")
			}
		}
	})
}

func TestLiveToolLoopGlobalBlobLimitRejectsAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		req := liveRequest(&liveFixtureHost{scope: scope})
		if _, err := e.Execute(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		e.liveMu.Lock()
		session := e.live[liveOwner(req)]
		session.retainedBytes.Store(256 << 20)
		e.liveMu.Unlock()
		req.Headers.Set("X-Opencode-Session-Id", "second-session")
		if _, err := e.Execute(context.Background(), req); err == nil || !strings.Contains(err.Error(), "blob capacity") {
			t.Fatalf("global retained blob limit=%v", err)
		}
	})
}

func TestLiveToolLoopInterruptDuringBatchDoesNotExposeCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan error, 1)
		go func() {
			response, err := e.Execute(ctx, liveRequest(&liveFixtureHost{scope: scope}))
			if len(response.Payload) != 0 {
				finished <- fmt.Errorf("cancelled batch escaped as response")
				return
			}
			finished <- err
		}()
		synctest.Wait()
		time.Sleep(200 * time.Millisecond)
		cancel()
		synctest.Wait()
		if err := <-finished; err == nil {
			t.Fatal("interrupted batch succeeded")
		}
		scope.mu.Lock()
		closed, parks := scope.closed, scope.parks
		scope.mu.Unlock()
		if !closed || parks != 0 {
			t.Fatal("interrupted batch was parked")
		}
	})
}
