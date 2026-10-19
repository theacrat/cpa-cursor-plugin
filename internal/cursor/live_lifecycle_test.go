package cursor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestLiveToolLoopExpiresAbandonedParkedRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		finished := make(chan error, 1)
		go func() {
			_, err := e.Execute(context.Background(), liveRequest(&liveFixtureHost{scope: scope}))
			finished <- err
		}()
		synctest.Wait()
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		e.liveMu.Lock()
		remaining := len(e.live)
		e.liveMu.Unlock()
		scope.mu.Lock()
		closed := scope.closed
		scope.mu.Unlock()
		if remaining != 0 || !closed {
			t.Fatal("abandoned retained run survived expiry")
		}
	})
}

func TestLiveToolLoopCancellationClosesActiveUpstream(t *testing.T) {
	scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
	e := NewExecutor(DefaultConfig())
	warmFixtureVariants(e)
	defer e.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Execute(ctx, liveRequest(&liveFixtureHost{scope: scope})); err == nil {
		t.Fatal("cancelled request was admitted")
	}
}

func TestLiveToolLoopRejectsConcurrentRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		req := liveRequest(&liveFixtureHost{scope: scope})
		first := make(chan error, 1)
		go func() { _, err := e.Execute(context.Background(), req); first <- err }()
		synctest.Wait()
		if _, err := e.Execute(context.Background(), req); err == nil || !strings.Contains(err.Error(), "active request") {
			t.Fatalf("concurrent resume admitted: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
	})
}

func TestLiveToolLoopResultsRequireOwnedLatestBatch(t *testing.T) {
	s := &liveSession{expected: []chatMessage{{Role: "user", Content: json.RawMessage(`"task"`)}}, pending: []livePending{{call: toolCall{ID: "pending"}}}}
	for _, tail := range []string{`[{"role":"tool","tool_call_id":"foreign","content":"result"}]`, `[]`, `[{"role":"tool","tool_call_id":"pending","content":"result"},{"role":"tool","tool_call_id":"pending","content":"duplicate"}]`} {
		var messages []chatMessage
		_ = json.Unmarshal([]byte(tail), &messages)
		raw, _ := json.Marshal(map[string]any{"messages": append(append([]chatMessage(nil), s.expected...), messages...)})
		if _, err := s.results(raw); err == nil {
			t.Fatal("invalid latest batch accepted")
		}
	}
}

var _ pluginapi.HostHTTPClient = (*liveFixtureHost)(nil)
