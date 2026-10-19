package cursor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestLiveToolLoopRestoresOnlyProvenOmittedCallsBeforeReplayValidation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streaming"}[streaming], func(t *testing.T) {
			scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
			e := NewExecutor(DefaultConfig())
			warmFixtureVariants(e)
			defer e.Close()
			req := liveRequest(&liveFixtureHost{scope: scope})
			first, err := e.Execute(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Choices []struct{ Message chatMessage }
			}
			if json.Unmarshal(first.Payload, &body) != nil {
				t.Fatal("invalid initial response")
			}
			message := body.Choices[0].Message
			id := message.ToolCalls[0].ID
			message.ToolCalls = nil
			message.Content = json.RawMessage(`null`)
			req.Payload, _ = json.Marshal(map[string]any{
				"messages": []chatMessage{{Role: "system", Content: json.RawMessage(`"Be brief"`)}, {Role: "user", Content: json.RawMessage(`"Hello"`)}, message, {Role: "tool", ToolCallID: id, Content: json.RawMessage(`"complete actual result"`)}},
				"tools":    []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}},
			})
			if streaming {
				req.Metadata["request_path"] = "/v1/chat/completions"
				response, err := e.ExecuteStream(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				var output strings.Builder
				for chunk := range response.Chunks {
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
					output.Write(chunk.Payload)
				}
				if !strings.Contains(output.String(), "continued after actual result") {
					t.Fatal("streaming continuation missing")
				}
			} else if _, err := e.Execute(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			scope.mu.Lock()
			defer scope.mu.Unlock()
			if scope.runs != 1 || scope.results != 1 || scope.attaches != 1 {
				t.Fatal("proven orphan result did not resume exactly its pending execution")
			}
		})
	}
}

func TestLiveToolLoopDoesNotRepairForeignOrphanResults(t *testing.T) {
	scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
	e := NewExecutor(DefaultConfig())
	warmFixtureVariants(e)
	defer e.Close()
	req := liveRequest(&liveFixtureHost{scope: scope})
	req.Payload = []byte(`{"messages":[{"role":"user","content":"task"},{"role":"assistant","content":null},{"role":"tool","tool_call_id":"foreign","content":"result"}]}`)
	if _, err := e.Execute(context.Background(), req); err == nil {
		t.Fatal("orphan without pending ownership was repaired")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.runs != 0 || scope.results != 0 {
		t.Fatal("foreign orphan reached upstream")
	}
}
