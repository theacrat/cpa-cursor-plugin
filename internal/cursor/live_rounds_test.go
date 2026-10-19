package cursor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
)

func TestLiveToolLoopMultipleRoundsNeverResendHistoricalResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8), rounds: 2}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		req := liveRequest(&liveFixtureHost{scope: scope})
		var payload map[string]json.RawMessage
		_ = json.Unmarshal(req.Payload, &payload)
		var messages []chatMessage
		_ = json.Unmarshal(payload["messages"], &messages)
		for round := 0; round < 2; round++ {
			response, err := e.Execute(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Choices []struct{ Message chatMessage }
			}
			if json.Unmarshal(response.Payload, &body) != nil || len(body.Choices[0].Message.ToolCalls) != 1 {
				t.Fatal("round did not expose exactly one call")
			}
			message := body.Choices[0].Message
			messages = append(messages, message, chatMessage{Role: "tool", ToolCallID: message.ToolCalls[0].ID, Content: json.RawMessage(`"completed actual result"`)})
			payload["messages"], _ = json.Marshal(messages)
			req.Payload, _ = json.Marshal(payload)
		}
		response, err := e.Execute(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(response.Payload), "continued after actual result") {
			t.Fatal("final continuation missing")
		}
		scope.mu.Lock()
		defer scope.mu.Unlock()
		if scope.runs != 1 || scope.results != 2 || scope.attaches != 2 || scope.parks != 2 {
			t.Fatalf("multi-round lifecycle runs=%d results=%d attaches=%d parks=%d", scope.runs, scope.results, scope.attaches, scope.parks)
		}
	})
}

func TestLiveToolLoopLateCallsResetBatchWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		finished := make(chan pluginapi.ExecutorResponse, 1)
		failures := make(chan error, 1)
		go func() {
			response, err := e.Execute(context.Background(), liveRequest(&liveFixtureHost{scope: scope}))
			if err != nil {
				failures <- err
				return
			}
			finished <- response
		}()
		synctest.Wait()
		time.Sleep(200 * time.Millisecond)
		scope.emit(t, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 8, ExecId: "upstream-exec", Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ProviderIdentifier: "cpa", ToolCallId: "late-call"}}}}})
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		select {
		case <-finished:
			t.Fatal("batch closed before the late-call window")
		case err := <-failures:
			t.Fatal(err)
		default:
		}
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()
		select {
		case response := <-finished:
			var body struct {
				Choices []struct{ Message chatMessage }
			}
			_ = json.Unmarshal(response.Payload, &body)
			if len(body.Choices[0].Message.ToolCalls) != 2 {
				t.Fatal("late call was lost")
			}
		case err := <-failures:
			t.Fatal(err)
		default:
			t.Fatal("batch did not close after the final debounce")
		}
	})
}
