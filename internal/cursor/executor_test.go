package cursor

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

var _ pluginapi.ProviderExecutor = (*Executor)(nil)

func testRequest() pluginapi.ExecutorRequest {
	return pluginapi.ExecutorRequest{Model: "fixture", Format: "openai", StorageJSON: []byte(`{"access_token":"fixture-secret","selected_team_id":"team"}`), Payload: []byte(`{"messages":[{"role":"system","content":"Be brief"},{"role":"user","content":"Hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`)}
}
func writeServer(t *testing.T, w http.ResponseWriter, msg *pb.AgentServerMessage) {
	t.Helper()
	raw, err := frameMessage(msg)
	if err != nil {
		t.Error(err)
		return
	}
	if _, err = w.Write(raw); err != nil {
		t.Error(err)
	}
	w.(http.Flusher).Flush()
}
func readClient(t *testing.T, r io.Reader) *pb.AgentClientMessage {
	t.Helper()
	_, raw, err := readFrame(r, "")
	if err != nil {
		t.Error(err)
		return &pb.AgentClientMessage{}
	}
	msg := &pb.AgentClientMessage{}
	if err := proto.Unmarshal(raw, msg); err != nil {
		t.Error(err)
	}
	return msg
}
func update(u *pb.InteractionUpdate) *pb.AgentServerMessage {
	return &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionUpdate{InteractionUpdate: u}}
}
func endTurn() *pb.AgentServerMessage {
	return update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TurnEnded{TurnEnded: &pb.TurnEndedUpdate{}}})
}

func tlsFixture(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Executor) {
	t.Helper()
	s := httptest.NewUnstartedServer(handler)
	s.EnableHTTP2 = true
	s.StartTLS()
	e := NewExecutor(Config{Enabled: true, AgentBaseURL: s.URL, APIBaseURL: s.URL, RequestTimeout: 5})
	e.client = s.Client()
	t.Cleanup(func() { e.Close(); s.Close() })
	return s, e
}

func TestNativeHTTP2Duplex(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.URL.Path != "/agent.v1.AgentService/Run" {
			t.Errorf("wrong transport %s %s", r.Proto, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("X-Cursor-Team-Id") != "team" {
			t.Error("missing credential headers")
		}
		initial := readClient(t, r.Body).GetRunRequest()
		if initial == nil {
			t.Error("missing run")
			return
		}
		if initial.RequestedModel.ModelId != "fixture" || initial.ModelDetails != nil || initial.CustomSystemPrompt != nil {
			t.Error("wrong model publication")
		}
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		id := initial.ConversationState.RootPromptMessagesJson[1]
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_KvServerMessage{KvServerMessage: &pb.KvServerMessage{Id: 10, Message: &pb.KvServerMessage_GetBlobArgs{GetBlobArgs: &pb.GetBlobArgs{BlobId: id}}}}})
		data := readClient(t, r.Body).GetKvClientMessage().GetGetBlobResult().BlobData
		if !bytes.Contains(data, []byte("rules")) || !bytes.Contains(data, []byte("Be brief")) {
			t.Errorf("rules not published: %s", data)
		}
		blob := []byte("checkpoint fixture")
		hash := sha256.Sum256(blob)
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_KvServerMessage{KvServerMessage: &pb.KvServerMessage{Id: 11, Message: &pb.KvServerMessage_SetBlobArgs{SetBlobArgs: &pb.SetBlobArgs{BlobId: hash[:], BlobData: blob}}}}})
		if readClient(t, r.Body).GetKvClientMessage().GetSetBlobResult() == nil {
			t.Error("missing set result")
		}
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 12, ExecId: "context", Message: &pb.ExecServerMessage_RequestContextArgs{RequestContextArgs: &pb.RequestContextArgs{}}}}})
		context := readClient(t, r.Body).GetExecClientMessage().GetRequestContextResult().GetSuccess().RequestContext
		if len(context.Tools) != 1 || context.Tools[0].Name != "lookup" {
			t.Error("context catalogue missing")
		}
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 13, Message: &pb.ExecServerMessage_ShellArgs{ShellArgs: &pb.ShellArgs{Command: "must-not-run"}}}}})
		if readClient(t, r.Body).GetExecClientControlMessage().GetThrow().Id != 13 {
			t.Error("native execution not explicitly rejected")
		}
		writeServer(t, w, update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_ThinkingDelta{ThinkingDelta: &pb.ThinkingDeltaUpdate{Text: "reason"}}}))
		writeServer(t, w, update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TextDelta{TextDelta: &pb.TextDeltaUpdate{Text: "answer"}}}))
		writeServer(t, w, update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TokenDelta{TokenDelta: &pb.TokenDeltaUpdate{Tokens: 3}}}))
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ConversationCheckpointUpdate{ConversationCheckpointUpdate: &pb.ConversationStateStructure{TokenDetails: &pb.ConversationTokenDetails{UsedTokens: 9}}}})
		writeServer(t, w, endTurn())
	})
	response, err := e.Execute(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content   string
				Reasoning string `json:"reasoning_content"`
			}
			Finish string `json:"finish_reason"`
		}
		Usage map[string]int
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Choices[0].Message.Content != "answer" || result.Choices[0].Message.Reasoning != "reason" || result.Usage["prompt_tokens"] != 6 || result.Usage["completion_tokens"] != 3 {
		t.Fatalf("wrong output %s", response.Payload)
	}
}

func TestMCPToolCallAndTranscriptContinuation(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		readClient(t, r.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		value, _ := structpb.NewValue("example")
		raw, _ := proto.Marshal(value)
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 1, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ToolCallId: "call-one", Args: map[string][]byte{"query": raw}}}}}})
	})
	response, err := e.Execute(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response.Payload, []byte(`"finish_reason":"tool_calls"`)) || !bytes.Contains(response.Payload, []byte(`"name":"lookup"`)) {
		t.Fatalf("wrong tool output %s", response.Payload)
	}
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"system","content":"Be brief"},{"role":"user","content":"Search"},{"role":"assistant","tool_calls":[{"id":"call-one","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"example\"}"}}]},{"role":"tool","tool_call_id":"call-one","content":"found it"}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`)
	built, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	run := built.message.GetRunRequest()
	if run.Action.GetResumeAction() == nil {
		t.Fatal("tool continuation must resume rebuilt transcript")
	}
	var root []string
	for _, id := range run.ConversationState.RootPromptMessagesJson {
		root = append(root, string(built.blobs.data[hex.EncodeToString(id)]))
	}
	joined := strings.Join(root, "\n")
	for _, want := range []string{"tool-call", "tool-result", "found it", "mcp_cpa_lookup", "Search"} {
		if !strings.Contains(joined, want) {
			t.Errorf("root prompt missing %s", want)
		}
	}
	turn := &pb.ConversationTurnStructure{}
	if err := proto.Unmarshal(built.blobs.data[hex.EncodeToString(run.ConversationState.Turns[0])], turn); err != nil {
		t.Fatal(err)
	}
	step := &pb.ConversationStep{}
	if err := proto.Unmarshal(built.blobs.data[hex.EncodeToString(turn.GetAgentConversationTurn().Steps[0])], step); err != nil {
		t.Fatal(err)
	}
	if step.GetToolCall().GetMcpToolCall().Result.GetSuccess().Content[0].GetText().Text != "found it" {
		t.Fatal("tool result missing from state")
	}
}

func TestStreamAndCloseDrain(t *testing.T) {
	started := make(chan struct{})
	_, e := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		readClient(t, r.Body)
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	stream, err := e.ExecuteStream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan struct{})
	go func() { e.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not drain")
	}
	for range stream.Chunks {
	}
	if _, err = e.Execute(context.Background(), testRequest()); err == nil {
		t.Fatal("closed executor admitted a run")
	}
}

func TestFramesGzipFragmentationAndLimits(t *testing.T) {
	payload := []byte("message")
	var zipped bytes.Buffer
	gz := gzip.NewWriter(&zipped)
	_, _ = gz.Write(payload)
	_ = gz.Close()
	frame := make([]byte, 5)
	frame[0] = 1
	binary.BigEndian.PutUint32(frame[1:], uint32(zipped.Len()))
	frame = append(frame, zipped.Bytes()...)
	flags, data, err := readFrame(&oneByteReader{r: bytes.NewReader(frame)}, "gzip")
	if err != nil || flags != 1 || !bytes.Equal(data, payload) {
		t.Fatalf("compressed fragmented frame: %d %s %v", flags, data, err)
	}
	frame[0] = 4
	if _, _, err := readFrame(bytes.NewReader(frame), "gzip"); err == nil {
		t.Fatal("unknown flags accepted")
	}
	frame[0] = 0
	binary.BigEndian.PutUint32(frame[1:], maxFrame+1)
	if _, _, err := readFrame(bytes.NewReader(frame), ""); err == nil {
		t.Fatal("oversized frame accepted")
	}
	var bomb bytes.Buffer
	gz = gzip.NewWriter(&bomb)
	_, _ = io.Copy(gz, io.LimitReader(zeroReader{}, maxFrame+1))
	_ = gz.Close()
	frame = make([]byte, 5)
	frame[0] = 1
	binary.BigEndian.PutUint32(frame[1:], uint32(bomb.Len()))
	frame = append(frame, bomb.Bytes()...)
	if _, _, err := readFrame(bytes.NewReader(frame), "gzip"); err == nil {
		t.Fatal("gzip bomb accepted")
	}
}

type oneByteReader struct{ r io.Reader }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.r.Read(p)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestRequestImagesNamesAndUnsupported(t *testing.T) {
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AQID"}}]}],"tools":[{"type":"function","function":{"name":"lookup"}},{"type":"function","function":{"name":"mcp_cpa_lookup"}}]}`)
	built, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if built.resolve["mcp_cpa_lookup"] != "lookup" || built.resolve["mcp_cpa_lookup_2"] != "mcp_cpa_lookup" {
		t.Fatal("tool namespace collision")
	}
	user := built.message.GetRunRequest().Action.GetUserMessageAction().UserMessage
	if !bytes.Equal(user.SelectedContext.SelectedImages[0].GetData(), []byte{1, 2, 3}) {
		t.Fatal("image lost")
	}
	for _, payload := range []string{`{"messages":[{"role":"user","content":"hi"}],"tool_choice":"required"}`, `{"messages":[{"role":"tool","tool_call_id":"missing","content":"result"}]}`, `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image"}}]}]}`} {
		req.Payload = []byte(payload)
		if _, err := buildRequest(req); err == nil {
			t.Errorf("unsupported request accepted: %s", payload)
		}
	}
	e := NewExecutor(Config{})
	if _, err := e.CountTokens(context.Background(), req); err == nil {
		t.Fatal("fake token count")
	}
	if _, err := e.HttpRequest(context.Background(), pluginapi.ExecutorHTTPRequest{}); err == nil {
		t.Fatal("arbitrary forwarding")
	}
	e.Close()
}

type fixtureHost struct {
	do     func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error)
	stream func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error)
}

func (h fixtureHost) Do(ctx context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	return h.do(ctx, r)
}
func (h fixtureHost) DoStream(ctx context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return h.stream(ctx, r)
}

func TestHostRunSSEBidiAppend(t *testing.T) {
	var mu sync.Mutex
	var requestID string
	seq := uint64(0)
	chunks := make(chan pluginapi.HTTPStreamChunk, 8)
	h := fixtureHost{
		stream: func(ctx context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
			if !strings.HasSuffix(r.URL, "/agent.v1.AgentService/RunSSE") || r.Headers.Get("X-Cursor-Streaming") != "true" {
				t.Error("wrong RunSSE request")
			}
			_, raw, err := readFrame(bytes.NewReader(r.Body), "")
			if err != nil {
				t.Fatal(err)
			}
			id := &pb.BidiRequestId{}
			if err := proto.Unmarshal(raw, id); err != nil {
				t.Fatal(err)
			}
			requestID = id.RequestId
			if requestID != r.Headers.Get("X-Request-Id") {
				t.Error("request ID differs")
			}
			return pluginapi.HTTPStreamResponse{StatusCode: 200, Headers: http.Header{}, Chunks: chunks}, nil
		},
		do: func(ctx context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			if !strings.HasSuffix(r.URL, "/aiserver.v1.BidiService/BidiAppend") || r.Headers.Get("X-Request-Id") != requestID {
				t.Error("wrong append path or ID")
			}
			fields := wireFields(t, r.Body)
			raw, err := hex.DecodeString(string(fields[1]))
			if err != nil {
				t.Fatal(err)
			}
			msg := &pb.AgentClientMessage{}
			if err := proto.Unmarshal(raw, msg); err != nil {
				t.Fatal(err)
			}
			value, n := protowire.ConsumeVarint(fields[3])
			if n < 0 || value != seq {
				t.Errorf("append sequence %d want %d", value, seq)
			}
			seq++
			id := &pb.BidiRequestId{}
			if err := proto.Unmarshal(fields[2], id); err != nil || id.RequestId != requestID {
				t.Error("append ID differs")
			}
			if run := msg.GetRunRequest(); run != nil {
				reply := &pb.AgentServerMessage{Message: &pb.AgentServerMessage_KvServerMessage{KvServerMessage: &pb.KvServerMessage{Id: 1, Message: &pb.KvServerMessage_GetBlobArgs{GetBlobArgs: &pb.GetBlobArgs{BlobId: run.ConversationState.RootPromptMessagesJson[1]}}}}}
				frame, _ := frameMessage(reply)
				chunks <- pluginapi.HTTPStreamChunk{Payload: frame[:2]}
				chunks <- pluginapi.HTTPStreamChunk{Payload: frame[2:]}
			} else if msg.GetKvClientMessage() != nil {
				frame, _ := frameMessage(endTurn())
				chunks <- pluginapi.HTTPStreamChunk{Payload: frame}
				close(chunks)
			} else {
				t.Error("unexpected append message")
			}
			return pluginapi.HTTPResponse{StatusCode: 200}, nil
		}}
	req := testRequest()
	req.HTTPClient = h
	e := NewExecutor(Config{Enabled: true})
	defer e.Close()
	if _, err := e.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Errorf("got %d appends", seq)
	}
}
func wireFields(t *testing.T, raw []byte) map[protowire.Number][]byte {
	t.Helper()
	fields := map[protowire.Number][]byte{}
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			t.Fatal("bad protobuf tag")
		}
		raw = raw[n:]
		switch typ {
		case protowire.BytesType:
			value, n := protowire.ConsumeBytes(raw)
			if n < 0 {
				t.Fatal("bad bytes")
			}
			fields[num] = value
			raw = raw[n:]
		case protowire.VarintType:
			_, n := protowire.ConsumeVarint(raw)
			if n < 0 {
				t.Fatal("bad varint")
			}
			fields[num] = raw[:n]
			raw = raw[n:]
		default:
			t.Fatal("unsupported test wire type")
		}
	}
	return fields
}

func TestModelsUseHostAndOfficialPath(t *testing.T) {
	req := pluginapi.AuthModelRequest{StorageJSON: testRequest().StorageJSON, HTTPClient: fixtureHost{do: func(ctx context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		if !strings.HasSuffix(r.URL, "/aiserver.v1.AiService/GetUsableModels") {
			t.Errorf("wrong discovery path %s", r.URL)
		}
		raw, _ := proto.Marshal(&pb.GetUsableModelsResponse{Models: []*pb.ModelDetails{{ModelId: "model", DisplayName: "Model"}}})
		return pluginapi.HTTPResponse{StatusCode: 200, Body: raw}, nil
	}}}
	e := NewExecutor(Config{Enabled: true})
	defer e.Close()
	response, err := e.ModelsForAuth(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Models) != 1 || response.Models[0].ID != "model" {
		t.Fatalf("wrong discovery %v", response)
	}
}
