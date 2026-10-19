package cursor

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

var replayToolCollisions = [][2]string{
	{"read", "mcp_cpa_read"},
	{"read", "mcp_cpa_mcp_cpa_read"},
	{"mcp_cpa_read", "mcp_cpa_mcp_cpa_read"},
	{"cpa-read", "read"},
	{"cpa-mcp_cpa_read", "read"},
	{"read", "cpa-mcp_cpa_read"},
}

func replayToolRequest(historic, current string, enabled bool) pluginapi.ExecutorRequest {
	req := testRequest()
	choice := ""
	if !enabled {
		choice = `,"tool_choice":"none"`
	}
	req.Payload = []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":"old task"},{"role":"assistant","tool_calls":[{"id":"historic","type":"function","function":{"name":%q,"arguments":"{\"path\":\"/client/file\"}"}}]},{"role":"tool","tool_call_id":"historic","content":"completed historic result"},{"role":"assistant","content":"finished"},{"role":"user","content":"summarise"}],"tools":[{"type":"function","function":{"name":%q,"parameters":{"type":"object"}}}]%s}`, historic, current, choice))
	return req
}

func replayedToolWire(t *testing.T, b *builtRequest) string {
	t.Helper()
	turn := &pb.ConversationTurnStructure{}
	state := b.message.GetRunRequest().ConversationState
	if len(state.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(state.Turns))
	}
	if err := proto.Unmarshal(b.blobs.data[hex.EncodeToString(state.Turns[0])], turn); err != nil {
		t.Fatal(err)
	}
	step := &pb.ConversationStep{}
	if err := proto.Unmarshal(b.blobs.data[hex.EncodeToString(turn.GetAgentConversationTurn().Steps[0])], step); err != nil {
		t.Fatal(err)
	}
	call := step.GetToolCall().GetMcpToolCall()
	result := call.GetResult().GetSuccess()
	if len(result.GetContent()) != 1 || result.Content[0].GetText().GetText() != "completed historic result" {
		t.Fatal("completed historic result missing from replay")
	}
	args := call.GetArgs()
	if args.GetName() == "" || args.GetName() != args.GetToolName() || args.GetProviderIdentifier() != "cpa" || args.GetToolCallId() != "historic" {
		t.Fatalf("historical protobuf identity changed: %v", args)
	}
	return call.GetArgs().GetToolName()
}

func TestReplayToolIdentitiesRemainDistinctFromCurrentCatalogue(t *testing.T) {
	for _, names := range replayToolCollisions {
		t.Run(names[0]+"/"+names[1], func(t *testing.T) {
			b, err := buildRequest(replayToolRequest(names[0], names[1], true))
			if err != nil {
				t.Fatal(err)
			}
			wire := replayedToolWire(t, b)
			for _, alias := range clientToolAliases(wire) {
				if got := b.resolve[alias]; got != "" {
					t.Errorf("historical-only alias %q resolves to current function %q", alias, got)
				}
			}
			for _, alias := range clientToolAliases(b.tools[0].Name) {
				if got := b.resolve[alias]; got != names[1] {
					t.Errorf("current alias %q resolves to %q, want %q", alias, got, names[1])
				}
			}
			var roots int
			for _, id := range b.message.GetRunRequest().ConversationState.RootPromptMessagesJson {
				var root struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				}
				if err := json.Unmarshal(b.blobs.data[hex.EncodeToString(id)], &root); err != nil {
					t.Fatal(err)
				}
				if root.Role != "assistant" && root.Role != "tool" {
					continue
				}
				var parts []struct {
					ToolName string `json:"toolName"`
				}
				if err := json.Unmarshal(root.Content, &parts); err != nil {
					t.Fatal(err)
				}
				for _, part := range parts {
					if part.ToolName == "" {
						continue
					}
					roots++
					if part.ToolName != "mcp_cpa_"+wire || part.ToolName == advertisedClientTools(b.tools)[0] {
						t.Errorf("historical root identity %q overlaps catalogue or differs from protobuf identity %q", part.ToolName, wire)
					}
				}
			}
			if roots != 2 {
				t.Fatalf("historical tool roots = %d, want 2", roots)
			}
		})
	}
}

func TestReplayToolAliasesCannotInvokeCurrentFunctions(t *testing.T) {
	for _, names := range replayToolCollisions {
		for _, field := range []string{"name", "tool_name"} {
			for aliasIndex := range 4 {
				t.Run(fmt.Sprintf("%s/%s/%s/%d", names[0], names[1], field, aliasIndex), func(t *testing.T) {
					req := replayToolRequest(names[0], names[1], true)
					b, err := buildRequest(req)
					if err != nil {
						t.Fatal(err)
					}
					wire := replayedToolWire(t, b)
					argsFor := func(alias string) *pb.McpArgs {
						args := &pb.McpArgs{ProviderIdentifier: "cpa", ToolCallId: "current-call"}
						if field == "name" {
							args.Name = alias
						} else {
							args.ToolName = alias
						}
						return args
					}
					handled := make(chan struct{})
					_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
						defer close(handled)
						readClient(t, request.Body)
						w.Header().Set("Content-Type", "application/connect+proto")
						w.WriteHeader(200)
						w.(http.Flusher).Flush()
						for i, alias := range clientToolAliases(wire) {
							writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: uint32(i + 1), Message: &pb.ExecServerMessage_McpArgs{McpArgs: argsFor(alias)}}}})
							if readClient(t, request.Body).GetExecClientMessage().GetMcpResult().GetToolNotFound() == nil {
								t.Errorf("historical-only alias %q was not rejected", alias)
								return
							}
						}
						args := argsFor(clientToolAliases(b.tools[0].Name)[aliasIndex])
						value, _ := structpb.NewValue("/current/file")
						raw, _ := proto.Marshal(value)
						args.Args = map[string][]byte{"path": raw}
						writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 5, Message: &pb.ExecServerMessage_McpArgs{McpArgs: args}}}})
					})
					response, err := e.Execute(context.Background(), req)
					<-handled
					if err != nil {
						t.Fatal(err)
					}
					var body struct {
						Choices []struct {
							Message struct {
								ToolCalls []toolCall `json:"tool_calls"`
							} `json:"message"`
						} `json:"choices"`
					}
					if err := json.Unmarshal(response.Payload, &body); err != nil {
						t.Fatal(err)
					}
					if len(body.Choices) != 1 || len(body.Choices[0].Message.ToolCalls) != 1 {
						t.Fatalf("missing current call: %s", response.Payload)
					}
					call := body.Choices[0].Message.ToolCalls[0]
					if call.ID != "current-call" || call.Function.Name != names[1] || call.Function.Arguments != `{"path":"/current/file"}` {
						t.Fatalf("current call identity or arguments changed: %s", response.Payload)
					}
				})
			}
		}
	}
}

func TestDisabledReplayToolsHaveNoExecutableAliases(t *testing.T) {
	for _, names := range replayToolCollisions {
		t.Run(names[0]+"/"+names[1], func(t *testing.T) {
			req := replayToolRequest(names[0], names[1], false)
			b, err := buildRequest(req)
			if err != nil {
				t.Fatal(err)
			}
			if len(b.resolve) != 0 || len(b.tools) != 0 {
				t.Errorf("disabled catalogue contains executable aliases: %v", b.resolve)
			}
			handled := make(chan struct{})
			_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
				defer close(handled)
				readClient(t, request.Body)
				w.Header().Set("Content-Type", "application/connect+proto")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				aliases := append(clientToolAliases(replayedToolWire(t, b)), clientToolAliases(b.wire[names[1]])...)
				for i, alias := range aliases {
					writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: uint32(i + 1), Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: alias, ProviderIdentifier: "cpa"}}}}})
					missing := readClient(t, request.Body).GetExecClientMessage().GetMcpResult().GetToolNotFound()
					if missing == nil || len(missing.AvailableTools) != 0 {
						t.Errorf("disabled alias %q was not rejected without alternatives", alias)
						return
					}
				}
				writeServer(t, w, endTurn())
			})
			response, err := e.Execute(context.Background(), req)
			<-handled
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(response.Payload, []byte(`"tool_calls"`)) {
				t.Fatalf("disabled tool dispatched: %s", response.Payload)
			}
		})
	}
}

func TestReplayToolKeepsCurrentIdentityForSameOriginalFunction(t *testing.T) {
	for _, name := range []string{"read", "mcp_cpa_mcp_cpa_read", "cpa-mcp_cpa_read"} {
		t.Run(name, func(t *testing.T) {
			b, err := buildRequest(replayToolRequest(name, name, true))
			if err != nil {
				t.Fatal(err)
			}
			wire := replayedToolWire(t, b)
			if wire != b.tools[0].Name {
				t.Fatalf("same original function has different identities: %q and %q", wire, b.tools[0].Name)
			}
			for _, alias := range clientToolAliases(wire) {
				if b.resolve[alias] != name {
					t.Errorf("same original function lost executable alias %q", alias)
				}
			}
		})
	}
}

func TestReplayToolRejectsEmptyOriginalFunctionName(t *testing.T) {
	if _, err := buildRequest(replayToolRequest("", "read", true)); err == nil {
		t.Fatal("empty historical function name was assigned a tool identity")
	}
}
