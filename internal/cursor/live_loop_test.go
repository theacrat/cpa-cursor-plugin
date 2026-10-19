package cursor

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type liveFixtureHost struct{ scope *liveFixtureScope }
type liveFixtureScope struct {
	mu                             sync.Mutex
	chunks                         chan pluginapi.HTTPStreamChunk
	runs, results, parks, attaches int
	closed                         bool
	batch                          int
	lastResult                     *pb.McpResult
	afterFirst                     bool
	duplicateExec                  bool
	rounds                         int
	firstResult                    *pb.McpResult
	arguments                      map[string][]byte
}

func (h *liveFixtureHost) SupportsRetainedHTTP() bool                           { return true }
func (h *liveFixtureHost) OpenScope(context.Context) (RetainedHTTPScope, error) { return h.scope, nil }
func (h *liveFixtureHost) AttachScope(context.Context, string) (RetainedHTTPScope, error) {
	h.scope.mu.Lock()
	h.scope.attaches++
	h.scope.mu.Unlock()
	return h.scope, nil
}
func (*liveFixtureHost) Do(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	return pluginapi.HTTPResponse{}, errors.New("unexpected request")
}
func (*liveFixtureHost) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return pluginapi.HTTPStreamResponse{}, errors.New("unexpected stream")
}
func (*liveFixtureScope) ID() string     { return "scope" }
func (s *liveFixtureScope) Close() error { s.mu.Lock(); s.closed = true; s.mu.Unlock(); return nil }
func (s *liveFixtureScope) PreparePark(context.Context) error {
	s.mu.Lock()
	s.parks++
	s.mu.Unlock()
	return nil
}
func (s *liveFixtureScope) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return pluginapi.HTTPStreamResponse{StatusCode: 200, Headers: http.Header{}, Chunks: s.chunks}, nil
}
func (s *liveFixtureScope) emit(t *testing.T, m *pb.AgentServerMessage) {
	t.Helper()
	frame, err := frameMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	s.chunks <- pluginapi.HTTPStreamChunk{Payload: frame}
}
func (s *liveFixtureScope) Do(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	_, _, n := protowire.ConsumeTag(req.Body)
	raw, _ := protowire.ConsumeString(req.Body[n:])
	decoded, _ := hex.DecodeString(raw)
	var m pb.AgentClientMessage
	if proto.Unmarshal(decoded, &m) != nil {
		return pluginapi.HTTPResponse{}, errors.New("invalid append")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.GetRunRequest() != nil {
		s.runs++
		if s.afterFirst && s.runs > 1 {
			for _, msg := range []*pb.AgentServerMessage{update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TextDelta{TextDelta: &pb.TextDeltaUpdate{Text: "fresh request completed"}}}), endTurn()} {
				frame, _ := frameMessage(msg)
				s.chunks <- pluginapi.HTTPStreamChunk{Payload: frame}
			}
			return pluginapi.HTTPResponse{StatusCode: 200}, nil
		}
		count := s.batch
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			execID := uint32(7 + index)
			if s.duplicateExec {
				execID = 7
			}
			frame, _ := frameMessage(&pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: execID, ExecId: "upstream-exec", Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ProviderIdentifier: "cpa", ToolCallId: fmt.Sprintf("upstream\ncall%d", index), Args: s.arguments}}}}})
			s.chunks <- pluginapi.HTTPStreamChunk{Payload: frame}
		}
	} else if result := m.GetExecClientMessage(); result != nil && result.GetMcpResult() != nil {
		if result.Id < 7 || result.Id > 8 || result.ExecId != "upstream-exec" {
			return pluginapi.HTTPResponse{}, errors.New("wrong pending execution")
		}
		s.results++
		if s.firstResult == nil {
			s.firstResult = proto.Clone(result.GetMcpResult()).(*pb.McpResult)
		}
		s.lastResult = proto.Clone(result.GetMcpResult()).(*pb.McpResult)
		if s.results < s.batch {
			return pluginapi.HTTPResponse{StatusCode: 200}, nil
		}
		if s.results < s.rounds {
			frame, _ := frameMessage(&pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: uint32(7 + s.results), ExecId: "upstream-exec", Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ProviderIdentifier: "cpa", ToolCallId: fmt.Sprintf("upstream\nround%d", s.results)}}}}})
			s.chunks <- pluginapi.HTTPStreamChunk{Payload: frame}
			return pluginapi.HTTPResponse{StatusCode: 200}, nil
		}
		for _, msg := range []*pb.AgentServerMessage{update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TextDelta{TextDelta: &pb.TextDeltaUpdate{Text: "continued after actual result"}}}), endTurn()} {
			frame, _ := frameMessage(msg)
			s.chunks <- pluginapi.HTTPStreamChunk{Payload: frame}
		}
	}
	return pluginapi.HTTPResponse{StatusCode: 200}, nil
}

func liveRequest(host pluginapi.HostHTTPClient) pluginapi.ExecutorRequest {
	req := testRequest()
	req.HTTPClient = host
	req.AuthID = "account"
	req.AuthProvider = "cursor"
	req.Metadata = map[string]any{"caller_scope": strings.Repeat("a", 64), "request_path": "/v1/chat/completions"}
	req.Headers = http.Header{"X-Opencode-Session-Id": {"session"}, "X-Cpa-Request-Kind": {"primary"}}
	return req
}

func TestLiveToolLoopKeepsOneUpstreamRunAcrossResults(t *testing.T) {
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
	if json.Unmarshal(first.Payload, &body) != nil || len(body.Choices[0].Message.ToolCalls) != 1 {
		t.Fatal("missing client tool call")
	}
	call := body.Choices[0].Message.ToolCalls[0]
	body.Choices[0].Message.Content = json.RawMessage(`null`)
	encoded, _ := json.Marshal(map[string]any{"messages": []chatMessage{{Role: "system", Content: json.RawMessage(`"Be brief"`)}, {Role: "user", Content: json.RawMessage(`"Hello"`)}, body.Choices[0].Message, {Role: "tool", ToolCallID: call.ID, Content: json.RawMessage(`"actual result"`)}}, "tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}})
	req.Payload = encoded
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
}

func TestLiveToolLoopBatchesEveryCallAndPreservesToolErrors(t *testing.T) {
	scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8), batch: 2}
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
		t.Fatal("invalid response")
	}
	calls := body.Choices[0].Message.ToolCalls
	if len(calls) != 2 || calls[0].ID == calls[1].ID {
		t.Fatalf("batch calls=%v", calls)
	}
	message := body.Choices[0].Message
	content := "<skill_content>" + strings.Repeat("complete result\n", 2000) + "</skill_content>"
	raw, _ := json.Marshal(content)
	messages := []chatMessage{{Role: "system", Content: json.RawMessage(`"Be brief"`)}, {Role: "user", Content: json.RawMessage(`"Hello"`)}, message, {Role: "tool", ToolCallID: calls[0].ID, Content: raw}, {Role: "tool", ToolCallID: calls[1].ID, Content: json.RawMessage(`"actual failure"`), IsError: true}}
	req.Payload, _ = json.Marshal(map[string]any{"messages": messages, "tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}})
	if _, err := e.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.runs != 1 || scope.results != 2 || scope.lastResult.GetError().GetError() != "actual failure" {
		t.Fatal("batch result ownership or actual error lost")
	}
	if scope.firstResult.GetSuccess().GetContent()[0].GetText().GetText() != content {
		t.Fatal("large skill content was truncated or changed")
	}
}

func TestLiveToolLoopRejectsDuplicateResultsBeforeWritingAny(t *testing.T) {
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
	_ = json.Unmarshal(first.Payload, &body)
	message := body.Choices[0].Message
	id := message.ToolCalls[0].ID
	req.Payload, _ = json.Marshal(map[string]any{"messages": []chatMessage{{Role: "system", Content: json.RawMessage(`"Be brief"`)}, {Role: "user", Content: json.RawMessage(`"Hello"`)}, message, {Role: "tool", ToolCallID: id, Content: json.RawMessage(`"result"`)}, {Role: "tool", ToolCallID: id, Content: json.RawMessage(`"result"`)}}, "tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}})
	if _, err := e.Execute(context.Background(), req); err == nil {
		t.Fatal("duplicate results were accepted")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.results != 0 {
		t.Fatal("partial invalid result batch was written")
	}
}

func TestLiveToolLoopNewUserInstructionRetiresPendingExecution(t *testing.T) {
	scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8), afterFirst: true}
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
	_ = json.Unmarshal(first.Payload, &body)
	message := body.Choices[0].Message
	id := message.ToolCalls[0].ID
	req.Payload, _ = json.Marshal(map[string]any{"messages": []chatMessage{{Role: "system", Content: json.RawMessage(`"Be brief"`)}, {Role: "user", Content: json.RawMessage(`"Hello"`)}, message, {Role: "tool", ToolCallID: id, Content: json.RawMessage(`"result"`)}, {Role: "user", Content: json.RawMessage(`"new instruction"`)}}, "tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}})
	response, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Payload), "fresh request completed") {
		t.Fatal("new user instruction did not start a fresh run")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.runs != 2 || scope.results != 0 {
		t.Fatal("new instruction injected a result into abandoned pending work")
	}
}

func TestLiveToolLoopStreamingRetainsResultOwnership(t *testing.T) {
	scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
	e := NewExecutor(DefaultConfig())
	warmFixtureVariants(e)
	defer e.Close()
	req := liveRequest(&liveFixtureHost{scope: scope})
	req.Metadata["request_path"] = "/v1/chat/completions"
	response, err := e.ExecuteStream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var calls []toolCall
	for chunk := range response.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		var body struct {
			Choices []struct {
				Delta struct {
					ToolCalls []toolCall `json:"tool_calls"`
				}
			}
		}
		if json.Unmarshal(chunk.Payload, &body) != nil {
			t.Fatal("invalid live stream chunk")
		}
		for _, choice := range body.Choices {
			calls = append(calls, choice.Delta.ToolCalls...)
		}
	}
	if len(calls) != 1 {
		t.Fatal("streaming live tool call missing")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.parks != 1 || scope.closed {
		t.Fatal("normal streamed tool response cancelled the retained run")
	}
}

func TestLiveToolLoopRejectsDuplicateExecutionIdentity(t *testing.T) {
	scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8), batch: 2, duplicateExec: true}
	e := NewExecutor(DefaultConfig())
	warmFixtureVariants(e)
	defer e.Close()
	response, err := e.Execute(context.Background(), liveRequest(&liveFixtureHost{scope: scope}))
	if err == nil || !strings.Contains(err.Error(), "execution identity") || len(response.Payload) > 0 {
		t.Fatal("duplicate execution identity escaped as client calls")
	}
}
