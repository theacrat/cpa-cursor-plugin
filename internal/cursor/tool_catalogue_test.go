package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	req.Metadata = map[string]any{"caller_scope": strings.Repeat("a", 64), "request_path": "/v1/chat/completions"}
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

func TestStatelessNewUserTurnRestoresOmittedCatalogue(t *testing.T) {
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
	var body struct {
		Choices []struct {
			Message struct {
				ToolCalls []toolCall `json:"tool_calls"`
			}
		}
	}
	if err := json.Unmarshal(second.Payload, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Choices) != 1 || len(body.Choices[0].Message.ToolCalls) != 1 || body.Choices[0].Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("new user turn lost the stored catalogue: %s", second.Payload)
	}
}

func TestStatelessExplicitToolCatalogueIsAuthoritative(t *testing.T) {
	store := newToolCatalogueStore()
	first := catalogueRequest()
	store.remember(first)
	for _, tools := range []string{`[]`, `null`} {
		req := catalogueRequest()
		req.Payload, _ = json.Marshal(map[string]any{
			"messages": []chatMessage{{Role: "tool", ToolCallID: "call-one", Content: json.RawMessage(`"result"`)}},
			"tools":    json.RawMessage(tools),
		})
		restored, err := store.restore(req)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(restored.Payload, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["tools"]) != tools {
			t.Fatalf("explicit tools %s was replaced with %s", tools, fields["tools"])
		}
	}
}

func TestStatelessCatalogueIsolatedAcrossProviderAndModelSwitches(t *testing.T) {
	store := newToolCatalogueStore()
	cursorRequest := catalogueRequest()
	store.remember(cursorRequest)

	for _, switched := range []pluginapi.ExecutorRequest{
		func() pluginapi.ExecutorRequest {
			req := catalogueRequest()
			req.AuthProvider = "grok"
			req.AuthID = "grok-account"
			req.Model = "grok-model"
			req.Payload = []byte(`{"messages":[{"role":"user","content":"continue"}]}`)
			return req
		}(),
		func() pluginapi.ExecutorRequest {
			req := catalogueRequest()
			req.Model = "cursor-other-model"
			req.Payload = []byte(`{"messages":[{"role":"user","content":"continue"}]}`)
			return req
		}(),
	} {
		restored, err := store.restore(switched)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(restored.Payload, &fields); err != nil {
			t.Fatal(err)
		}
		if _, exists := fields["tools"]; exists {
			t.Fatalf("switched provider or model inherited the Cursor catalogue: %s", restored.Payload)
		}
	}

	resumed, err := store.restore(cursorRequestWithPayload(cursorRequest, `{"messages":[{"role":"user","content":"back to cursor"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(resumed.Payload, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["tools"]; !exists {
		t.Fatal("returning to Cursor lost its own catalogue")
	}
}

func cursorRequestWithPayload(req pluginapi.ExecutorRequest, payload string) pluginapi.ExecutorRequest {
	req.Payload = []byte(payload)
	return req
}
