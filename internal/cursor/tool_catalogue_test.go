package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func catalogueRequest() pluginapi.ExecutorRequest {
	req := testRequest()
	req.AuthID = "account"
	req.AuthProvider = "cursor"
	req.Headers = http.Header{"X-Opencode-Session-Id": {"catalogue-session"}}
	req.Metadata = map[string]any{"caller_scope": "catalogue-caller", "request_path": "/v1/chat/completions"}
	return req
}

func TestStatelessOmittedToolsContinuationKeepsExecutableCatalogue(t *testing.T) {
	var n atomic.Int32
	_, e := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		readClient(t, r.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		value, _ := structpb.NewValue("example")
		raw, _ := proto.Marshal(value)
		round := n.Add(1)
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: uint32(round), Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ProviderIdentifier: "cpa", ToolCallId: fmt.Sprintf("upstream-call-%d", round), Args: map[string][]byte{"query": raw}}}}}})
		if round == 1 {
			return
		}
		_, frame, err := readFrame(r.Body, "")
		if err != nil {
			return
		}
		reply := new(pb.AgentClientMessage)
		if proto.Unmarshal(frame, reply) != nil {
			return
		}
		if reply.GetExecClientMessage().GetMcpResult().GetToolNotFound() != nil {
			writeServer(t, w, endTurn())
		}
	})
	first, err := e.Execute(context.Background(), catalogueRequest())
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Choices []struct{ Message chatMessage }
	}
	if json.Unmarshal(first.Payload, &body) != nil || len(body.Choices) != 1 || len(body.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("missing first client tool call: %s", first.Payload)
	}
	message := body.Choices[0].Message
	message.Content = json.RawMessage(`null`)
	req := catalogueRequest()
	req.Payload, _ = json.Marshal(map[string]any{
		"messages": []chatMessage{
			{Role: "system", Content: json.RawMessage(`"Be brief"`)},
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
			message,
			{Role: "tool", ToolCallID: message.ToolCalls[0].ID, Content: json.RawMessage(`"found it"`)},
		},
	})
	second, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(second.Payload, &body) != nil || len(body.Choices) != 1 || len(body.Choices[0].Message.ToolCalls) != 1 || body.Choices[0].Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("omitted-tools continuation lost the executable catalogue: %s", second.Payload)
	}
}

func TestStatelessNewUserTurnDoesNotReuseStoredCatalogue(t *testing.T) {
	var n atomic.Int32
	_, e := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		readClient(t, r.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		round := n.Add(1)
		if round == 1 {
			writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 1, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ProviderIdentifier: "cpa", ToolCallId: "first"}}}}})
			return
		}
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 2, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ProviderIdentifier: "cpa", ToolCallId: "second"}}}}})
		_, frame, err := readFrame(r.Body, "")
		if err != nil {
			return
		}
		reply := new(pb.AgentClientMessage)
		if proto.Unmarshal(frame, reply) != nil {
			return
		}
		if reply.GetExecClientMessage().GetMcpResult().GetToolNotFound() != nil {
			writeServer(t, w, endTurn())
		}
	})
	if _, err := e.Execute(context.Background(), catalogueRequest()); err != nil {
		t.Fatal(err)
	}
	req := catalogueRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":"summarise"}]}`)
	second, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(second.Payload) && json.Unmarshal(second.Payload, new(struct {
		Choices []struct {
			Message struct {
				ToolCalls []toolCall `json:"tool_calls"`
			}
		}
	})) == nil {
		var body struct {
			Choices []struct {
				Message struct {
					ToolCalls []toolCall `json:"tool_calls"`
				}
			}
		}
		_ = json.Unmarshal(second.Payload, &body)
		if len(body.Choices) > 0 && len(body.Choices[0].Message.ToolCalls) > 0 {
			t.Fatalf("new user turn reused a stored catalogue: %s", second.Payload)
		}
	}
}
