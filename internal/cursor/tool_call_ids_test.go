package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const grokCompositeToolID = "call-12a49f6f-f204-42d9-b920-1c48fb0e5fea-2\nfc_259dee6b-327d-918f-9281-a44ef143f1c6_0"

var safeToolCallIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func emittedSkillCall(t *testing.T, upstreamID string, stream bool) toolCall {
	t.Helper()
	handled := make(chan struct{})
	_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
		defer close(handled)
		readClient(t, request.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		value, _ := structpb.NewValue("using-agent-skills")
		raw, _ := proto.Marshal(value)
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 1, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_skill", ProviderIdentifier: "cpa", ToolCallId: upstreamID, Args: map[string][]byte{"id": raw}}}}}})
	})
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":"load the skill"}],"tools":[{"type":"function","function":{"name":"skill","parameters":{"type":"object"}}}]}`)
	req.Metadata = map[string]any{"request_path": "/v1/chat/completions"}
	var logs []string
	e.Log = func(line string) { logs = append(logs, line) }
	var payloads [][]byte
	if stream {
		response, err := e.ExecuteStream(context.Background(), req)
		if err != nil {
			<-handled
			t.Fatal(err)
		}
		for chunk := range response.Chunks {
			if chunk.Err != nil {
				t.Error(chunk.Err)
				continue
			}
			payloads = append(payloads, chunk.Payload)
		}
	} else {
		response, err := e.Execute(context.Background(), req)
		if err != nil {
			<-handled
			t.Fatal(err)
		}
		payloads = append(payloads, response.Payload)
	}
	<-handled
	if !safeToolCallIDPattern.MatchString(upstreamID) && upstreamID != "" && strings.Contains(strings.Join(logs, "\n"), upstreamID) {
		t.Fatal("upstream tool-call ID leaked into diagnostic logs")
	}
	var calls []toolCall
	for _, payload := range payloads {
		var body struct {
			Choices []struct {
				Message struct {
					ToolCalls []toolCall `json:"tool_calls"`
				} `json:"message"`
				Delta struct {
					ToolCalls []toolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatal(err)
		}
		for _, choice := range body.Choices {
			calls = append(calls, choice.Message.ToolCalls...)
			calls = append(calls, choice.Delta.ToolCalls...)
		}
	}
	if len(calls) != 1 || calls[0].Function.Name != "skill" || calls[0].Function.Arguments != `{"id":"using-agent-skills"}` {
		t.Fatalf("skill call identity or arguments changed: %v", calls)
	}
	return calls[0]
}

func TestToolCallIDBoundaryNormalisesCompositeGrokIDsInBothResponseModes(t *testing.T) {
	digest := sha256.Sum256([]byte(grokCompositeToolID))
	want := "call_" + base64.RawURLEncoding.EncodeToString(digest[:])
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "execute", true: "stream"}[stream], func(t *testing.T) {
			call := emittedSkillCall(t, grokCompositeToolID, stream)
			if call.ID != want || !safeToolCallIDPattern.MatchString(call.ID) {
				t.Errorf("unsafe upstream ID escaped the boundary: got %q, want %q", call.ID, want)
			}
		})
	}
}

func TestToolCallIDBoundaryPreservesSafeIDsAndHashesWholeUnsafeIDs(t *testing.T) {
	for name, raw := range map[string]string{
		"ordinary": "call_123-ABC", "one character": "a", "maximum safe": strings.Repeat("a", 64),
		"complete safe alphabet": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-",
		"overlong":               strings.Repeat("a", 65), "newline": "call-secret\nfc_secret_0", "carriage return": "call\rfc",
		"tab": "call\tfc", "nul": "call\x00fc", "unicode": "call_工具", "unicode line": "call\u2028fc", "unicode paragraph": "call\u2029fc",
		"space": "call fc", "quote": `call"fc`, "backslash": `call\fc`, "selector punctuation": "call[fc]:0", "blank": "   ",
	} {
		t.Run(name, func(t *testing.T) {
			want := raw
			if !safeToolCallIDPattern.MatchString(raw) {
				digest := sha256.Sum256([]byte(raw))
				want = "call_" + base64.RawURLEncoding.EncodeToString(digest[:])
			}
			for _, stream := range []bool{false, true} {
				call := emittedSkillCall(t, raw, stream)
				if call.ID != want || !safeToolCallIDPattern.MatchString(call.ID) {
					t.Errorf("wrong public ID: got %q, want %q", call.ID, want)
				}
			}
		})
	}
}

func TestToolCallIDBoundaryIsStableAcrossOrderAndKeepsDistinctTails(t *testing.T) {
	first, second := "call-shared\nfc_first_0", "call-shared\nfc_second_0"
	a := emittedSkillCall(t, first, false).ID
	b := emittedSkillCall(t, second, false).ID
	if emittedSkillCall(t, second, true).ID != b || emittedSkillCall(t, first, true).ID != a || a == b {
		t.Fatal("tool-call IDs depend on arrival order or ignore the composite tail")
	}
}

func TestToolCallIDBoundaryKeepsFreshFallbackForMissingIDs(t *testing.T) {
	first := emittedSkillCall(t, "", false).ID
	second := emittedSkillCall(t, "", true).ID
	if !safeToolCallIDPattern.MatchString(first) || !safeToolCallIDPattern.MatchString(second) || !strings.HasPrefix(first, "call_") || first == second {
		t.Fatal("missing tool-call IDs lost their fresh safe fallback")
	}
}

func TestToolCallIDBoundaryReplaysCompletedSkillResultsWithoutRewritingHistory(t *testing.T) {
	content := "<skill_content>" + strings.Repeat("Skill content is data.\n", 1000) + "</skill_content>"
	for _, id := range []string{emittedSkillCall(t, grokCompositeToolID, false).ID, grokCompositeToolID} {
		req := testRequest()
		payload := struct {
			Messages []chatMessage `json:"messages"`
		}{Messages: []chatMessage{
			{Role: "user", Content: json.RawMessage(`"load the skill"`)},
			{Role: "assistant", ToolCalls: []toolCall{{ID: id, Type: "function", Function: functionCall{Name: "skill", Arguments: `{"id":"using-agent-skills"}`}}}},
			{Role: "tool", ToolCallID: id},
		}}
		payload.Messages[2].Content, _ = json.Marshal(content)
		req.Payload, _ = json.Marshal(payload)
		b, err := buildRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		state := b.message.GetRunRequest().ConversationState
		if b.message.GetRunRequest().Action.GetResumeAction() == nil || len(state.Turns) != 1 {
			t.Fatal("completed skill exchange did not resume")
		}
		if len(b.tools) != 0 || len(b.resolve) != 0 {
			t.Fatal("replayed skill was made executable without a current catalogue")
		}
		var turn pb.ConversationTurnStructure
		if err := proto.Unmarshal(b.blobs.data[hex.EncodeToString(state.Turns[0])], &turn); err != nil {
			t.Fatal(err)
		}
		var step pb.ConversationStep
		if err := proto.Unmarshal(b.blobs.data[hex.EncodeToString(turn.GetAgentConversationTurn().Steps[0])], &step); err != nil {
			t.Fatal(err)
		}
		call := step.GetToolCall().GetMcpToolCall()
		items := call.GetResult().GetSuccess().GetContent()
		if call.GetArgs().GetToolCallId() != id || len(items) != 1 || items[0].GetText().GetText() != content {
			t.Fatal("protobuf replay changed the skill ID or large output")
		}
		matched := 0
		for _, blob := range state.RootPromptMessagesJson {
			var root struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(b.blobs.data[hex.EncodeToString(blob)], &root); err != nil {
				t.Fatal(err)
			}
			if root.Role != "assistant" && root.Role != "tool" {
				continue
			}
			var parts []struct {
				ToolCallID string `json:"toolCallId"`
				Result     string `json:"result"`
			}
			if err := json.Unmarshal(root.Content, &parts); err != nil {
				t.Fatal(err)
			}
			for _, part := range parts {
				if part.ToolCallID != id || root.Role == "tool" && part.Result != content {
					t.Error("root replay changed the skill ID or large output")
				}
				matched++
			}
		}
		if matched != 2 {
			t.Fatalf("skill replay root parts = %d, want 2", matched)
		}
	}
}

func TestToolCallIDBoundaryChecksEveryByteAndHashesInvalidUTF8(t *testing.T) {
	for character := 0; character <= 255; character++ {
		raw := string([]byte{byte(character)})
		got := clientToolCallID(raw)
		want := raw
		if !safeToolCallIDPattern.MatchString(raw) {
			digest := sha256.Sum256([]byte(raw))
			want = "call_" + base64.RawURLEncoding.EncodeToString(digest[:])
		}
		if got != want {
			t.Errorf("byte %d produced an unexpected public ID", character)
		}
	}
}
