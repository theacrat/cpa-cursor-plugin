package cursor

import (
	"context"
	"encoding/json"
	"testing"
	"testing/synctest"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type liveFallbackHost struct {
	*liveFixtureHost
	fallback *liveRecoveryScope
}

func (h *liveFallbackHost) Do(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	return h.fallback.Do(ctx, req)
}

func (h *liveFallbackHost) DoStream(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return h.fallback.DoStream(ctx, req)
}

func TestLiveToolLoopToolFreePrimaryDoesNotConsumeParkedCodingRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
		fallback := &liveRecoveryScope{liveFixtureScope: &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}, text: "tool-free completed", complete: true}
		host := &liveFallbackHost{liveFixtureHost: &liveFixtureHost{scope: scope}, fallback: fallback}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		req := liveRequest(host)
		if _, err := e.Execute(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		e.liveMu.Lock()
		original := e.live[liveOwner(req)]
		e.liveMu.Unlock()
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(req.Payload, &fields)
		delete(fields, "tools")
		req.Payload, _ = json.Marshal(fields)
		if _, err := e.Execute(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		e.liveMu.Lock()
		remaining := e.live[liveOwner(req)]
		e.liveMu.Unlock()
		scope.mu.Lock()
		closed, attaches := scope.closed, scope.attaches
		scope.mu.Unlock()
		if original == nil || remaining != original || closed || attaches != 0 {
			t.Fatal("tool-free request consumed a parked coding run")
		}
	})
}
