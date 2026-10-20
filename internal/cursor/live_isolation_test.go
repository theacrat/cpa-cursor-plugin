package cursor

import (
	"context"
	"encoding/json"
	"strings"
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

func TestLiveToolLoopToolResultContinuationKeepsParkedCatalogueWhenToolsOmitted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
		host := &liveFixtureHost{scope: scope}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		req := liveRequest(host)
		first, err := e.Execute(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Choices []struct{ Message chatMessage }
		}
		if err := json.Unmarshal(first.Payload, &body); err != nil || len(body.Choices) != 1 || len(body.Choices[0].Message.ToolCalls) != 1 {
			t.Fatal("missing client tool call")
		}
		message := body.Choices[0].Message
		message.Content = json.RawMessage(`null`)
		callID := message.ToolCalls[0].ID
		req.Payload, _ = json.Marshal(map[string]any{
			"messages": []chatMessage{
				{Role: "system", Content: json.RawMessage(`"Be brief"`)},
				{Role: "user", Content: json.RawMessage(`"Hello"`)},
				message,
				{Role: "tool", ToolCallID: callID, Content: json.RawMessage(`"actual result"`)},
			},
		})
		second, err := e.Execute(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(second.Payload), "continued after actual result") {
			t.Fatal("result did not resume upstream")
		}
		scope.mu.Lock()
		defer scope.mu.Unlock()
		if scope.runs != 1 || scope.results != 1 || scope.parks != 1 || scope.attaches != 1 {
			t.Fatalf("live run lifecycle runs=%d results=%d parks=%d attaches=%d", scope.runs, scope.results, scope.parks, scope.attaches)
		}
	})
}
