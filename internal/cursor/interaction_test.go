package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const interactionSecret = "interaction-secret-never-echo"

func interactionQueries() []*pb.InteractionQuery {
	webFetch := &pb.InteractionQuery{Id: 9}
	webFetch.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 9, protowire.BytesType), []byte(interactionSecret)))
	return []*pb.InteractionQuery{
		{Id: 2, Query: &pb.InteractionQuery_WebSearchRequestQuery{WebSearchRequestQuery: &pb.WebSearchRequestQuery{Args: &pb.WebSearchArgs{SearchTerm: interactionSecret}}}},
		{Id: 3, Query: &pb.InteractionQuery_AskQuestionInteractionQuery{AskQuestionInteractionQuery: &pb.AskQuestionInteractionQuery{Args: &pb.AskQuestionArgs{Title: interactionSecret}}}},
		{Id: 4, Query: &pb.InteractionQuery_SwitchModeRequestQuery{SwitchModeRequestQuery: &pb.SwitchModeRequestQuery{Args: &pb.SwitchModeArgs{TargetModeId: interactionSecret}}}},
		{Id: 5, Query: &pb.InteractionQuery_ExaSearchRequestQuery{ExaSearchRequestQuery: &pb.ExaSearchRequestQuery{Args: &pb.ExaSearchArgs{Query: interactionSecret}}}},
		{Id: 6, Query: &pb.InteractionQuery_ExaFetchRequestQuery{ExaFetchRequestQuery: &pb.ExaFetchRequestQuery{Args: &pb.ExaFetchArgs{Ids: []string{interactionSecret}}}}},
		{Id: 7, Query: &pb.InteractionQuery_CreatePlanRequestQuery{CreatePlanRequestQuery: &pb.CreatePlanRequestQuery{Args: &pb.CreatePlanArgs{Plan: interactionSecret}}}},
		webFetch,
	}
}

func assertInteractionRefusal(t *testing.T, query *pb.InteractionQuery, message *pb.AgentClientMessage) string {
	t.Helper()
	raw, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &pb.AgentClientMessage{}
	if err := proto.Unmarshal(raw, decoded); err != nil {
		t.Fatal(err)
	}
	response := decoded.GetInteractionResponse()
	if response == nil || response.Id != query.Id {
		t.Fatalf("missing correlated interaction refusal: %v", decoded)
	}
	var reason string
	switch query.Id {
	case 2:
		reason = response.GetWebSearchRequestResponse().GetRejected().GetReason()
	case 3:
		reason = response.GetAskQuestionInteractionResponse().GetResult().GetRejected().GetReason()
		if !strings.Contains(reason, "No answer or consent was collected") {
			t.Errorf("question refusal implies an answer or consent: %q", reason)
		}
	case 4:
		reason = response.GetSwitchModeRequestResponse().GetRejected().GetReason()
	case 5:
		reason = response.GetExaSearchRequestResponse().GetRejected().GetReason()
	case 6:
		reason = response.GetExaFetchRequestResponse().GetRejected().GetReason()
	case 7:
		result := response.GetCreatePlanRequestResponse().GetResult()
		reason = result.GetError().GetError()
		if result.GetPlanUri() != "" {
			t.Error("plan refusal fabricated a plan URI")
		}
	case 9:
		fields := wireFields(t, response.ProtoReflect().GetUnknown())
		rejected := wireFields(t, fields[9])
		if len(rejected) != 1 || rejected[2] == nil {
			t.Fatal("field 9 response is not exactly a rejection")
		}
		reasonFields := wireFields(t, rejected[2])
		if len(reasonFields) != 1 {
			t.Fatal("field 9 refusal has unexpected fields")
		}
		reason = string(reasonFields[1])
		expected := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), []byte(reason))
		expected = protowire.AppendBytes(protowire.AppendTag(nil, 2, protowire.BytesType), expected)
		expected = protowire.AppendBytes(protowire.AppendTag(nil, 9, protowire.BytesType), expected)
		interaction := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), uint64(query.Id))
		interaction = append(interaction, expected...)
		expected = protowire.AppendBytes(protowire.AppendTag(nil, 6, protowire.BytesType), interaction)
		if !bytes.Equal(raw, expected) {
			t.Fatal("field 9 refusal differs from verified negative wire shape")
		}
	}
	if reason == "" || strings.Contains(decoded.String(), "approved:") || strings.Contains(decoded.String(), "success:") || strings.Contains(string(raw), interactionSecret) {
		t.Fatalf("untruthful or unredacted interaction refusal: %v", decoded)
	}
	return reason
}

func TestInteractionRefusalsContinueToRegisteredClientTools(t *testing.T) {
	for _, query := range interactionQueries() {
		t.Run(fmt.Sprint(query.Id), func(t *testing.T) {
			handled := make(chan struct{})
			_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
				defer close(handled)
				readClient(t, request.Body)
				w.Header().Set("Content-Type", "application/connect+proto")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: query}})
				reason := assertInteractionRefusal(t, query, readClient(t, request.Body))
				if !strings.Contains(reason, "mcp_cpa_lookup") || !strings.Contains(reason, "schema") {
					t.Errorf("refusal lost real client alternatives: %q", reason)
				}
				value, _ := structpb.NewValue("client query")
				raw, _ := proto.Marshal(value)
				writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 12, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ProviderIdentifier: "cpa", ToolCallId: "real-client-call", Args: map[string][]byte{"query": raw}}}}}})
			})
			var logs []string
			e.Log = func(line string) { logs = append(logs, line) }
			response, err := e.Execute(context.Background(), testRequest())
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
				t.Fatalf("missing genuine client call: %s", response.Payload)
			}
			call := body.Choices[0].Message.ToolCalls[0]
			if call.ID != "real-client-call" || call.Function.Name != "lookup" || call.Function.Arguments != `{"query":"client query"}` {
				t.Fatalf("client identity or arguments changed: %s", response.Payload)
			}
			if strings.Contains(strings.Join(logs, "\n")+string(response.Payload), interactionSecret) {
				t.Fatal("interaction arguments leaked to client output or logs")
			}
		})
	}
}

func TestInteractionRefusalsDoNotInventToolFreeAlternatives(t *testing.T) {
	handled := make(chan struct{})
	_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
		defer close(handled)
		readClient(t, request.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for _, query := range interactionQueries() {
			writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: query}})
			reason := assertInteractionRefusal(t, query, readClient(t, request.Body))
			if !strings.Contains(reason, "No client tools are exposed") || strings.Contains(reason, "mcp_cpa_") {
				t.Errorf("tool-free refusal invented alternatives: %q", reason)
			}
		}
		writeServer(t, w, endTurn())
	})
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":"summarise"}],"tool_choice":"none","tools":[{"type":"function","function":{"name":"lookup"}}]}`)
	response, err := e.Execute(context.Background(), req)
	<-handled
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(response.Payload, []byte(`"tool_calls"`)) {
		t.Fatalf("tool-free interaction fabricated a tool call: %s", response.Payload)
	}
}

func TestInteractionRefusalsShareEightReplyBoundWithExecRejections(t *testing.T) {
	for _, last := range []string{"interaction", "native", "unavailable"} {
		t.Run(last, func(t *testing.T) {
			handled := make(chan struct{})
			_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
				defer close(handled)
				readClient(t, request.Body)
				w.Header().Set("Content-Type", "application/connect+proto")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				for i := 0; i <= maxRejectedToolCalls; i++ {
					kind := "interaction"
					if i == 2 {
						kind = "native"
					} else if i == 5 {
						kind = "unavailable"
					} else if i == maxRejectedToolCalls {
						kind = last
					}
					message := &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: interactionQueries()[0]}}
					if kind == "native" {
						message.Message = &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: uint32(i + 1), Message: &pb.ExecServerMessage_ReadArgs{ReadArgs: &pb.ReadArgs{Path: interactionSecret}}}}
					} else if kind == "unavailable" {
						message.Message = &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: uint32(i + 1), Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: interactionSecret}}}}
					}
					writeServer(t, w, message)
					if i == maxRejectedToolCalls {
						return
					}
					reply := readClient(t, request.Body)
					switch kind {
					case "native":
						if reply.GetExecClientMessage().GetReadResult().GetRejected() == nil {
							t.Error("native request was not rejected")
							return
						}
					case "unavailable":
						if reply.GetExecClientMessage().GetMcpResult().GetToolNotFound() == nil {
							t.Error("unavailable MCP request was not rejected")
							return
						}
					default:
						assertInteractionRefusal(t, interactionQueries()[0], reply)
					}
				}
			})
			_, err := e.Execute(context.Background(), testRequest())
			<-handled
			if err == nil || !strings.Contains(err.Error(), "repeatedly requested") || strings.Contains(err.Error(), interactionSecret) {
				t.Fatalf("mixed refusals escaped the shared bound or leaked arguments: %v", err)
			}
		})
	}
}

func TestInteractionUnsupportedQueriesFailClosedWithoutReplies(t *testing.T) {
	unknown := func(field protowire.Number, kind protowire.Type) *pb.InteractionQuery {
		query := &pb.InteractionQuery{Id: 44}
		raw := protowire.AppendTag(nil, field, kind)
		if kind == protowire.BytesType {
			raw = protowire.AppendBytes(raw, []byte(interactionSecret))
		} else {
			raw = protowire.AppendVarint(raw, 1)
		}
		query.ProtoReflect().SetUnknown(raw)
		return query
	}
	mixed := proto.Clone(interactionQueries()[0]).(*pb.InteractionQuery)
	mixed.ProtoReflect().SetUnknown(unknown(99, protowire.BytesType).ProtoReflect().GetUnknown())
	mixedWebFetch := proto.Clone(interactionQueries()[0]).(*pb.InteractionQuery)
	mixedWebFetch.ProtoReflect().SetUnknown(unknown(9, protowire.BytesType).ProtoReflect().GetUnknown())
	duplicate := unknown(9, protowire.BytesType)
	duplicate.ProtoReflect().SetUnknown(append(duplicate.ProtoReflect().GetUnknown(), duplicate.ProtoReflect().GetUnknown()...))
	unknownWebFetch := unknown(9, protowire.BytesType)
	unknownWebFetch.ProtoReflect().SetUnknown(append(unknownWebFetch.ProtoReflect().GetUnknown(), unknown(99, protowire.BytesType).ProtoReflect().GetUnknown()...))
	for _, query := range []*pb.InteractionQuery{
		{Id: 8, Query: &pb.InteractionQuery_SetupVmEnvironmentArgs{SetupVmEnvironmentArgs: &pb.SetupVmEnvironmentArgs{InstallCommand: interactionSecret, StartCommand: interactionSecret}}},
		{Id: 42}, unknown(99, protowire.BytesType), unknown(9, protowire.VarintType), mixed, mixedWebFetch, duplicate, unknownWebFetch,
	} {
		t.Run(fmt.Sprint(query.Id, "/", len(query.ProtoReflect().GetUnknown())), func(t *testing.T) {
			handled := make(chan struct{})
			_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
				defer close(handled)
				readClient(t, request.Body)
				w.Header().Set("Content-Type", "application/connect+proto")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: query}})
				_, raw, err := readFrame(request.Body, "")
				if err == nil || len(raw) != 0 {
					t.Error("unsupported interaction received a fabricated reply")
				}
			})
			var logs []string
			e.Log = func(line string) { logs = append(logs, line) }
			_, err := e.Execute(context.Background(), testRequest())
			<-handled
			if err == nil || !strings.Contains(err.Error(), "interaction") || strings.Contains(err.Error()+strings.Join(logs, "\n"), interactionSecret) {
				t.Fatalf("unsupported interaction did not fail safely: %v", err)
			}
		})
	}
}

func TestInteractionReplyRejectsNilAndMalformedWebFetch(t *testing.T) {
	malformed := &pb.InteractionQuery{Id: 9}
	malformed.ProtoReflect().SetUnknown([]byte{0x4a, 0xff})
	for _, query := range []*pb.InteractionQuery{nil, malformed} {
		if reply, err := interactionReply(query, nil); reply != nil || err == nil {
			t.Fatalf("invalid interaction got a reply: %v, %v", reply, err)
		}
	}
}

func TestInteractionRefusalsPreserveFullUint32CorrelationIDs(t *testing.T) {
	for _, query := range interactionQueries() {
		for _, id := range []uint32{0, 1, 1 << 31, ^uint32(0)} {
			query := proto.Clone(query).(*pb.InteractionQuery)
			query.Id = id
			reply, err := interactionReply(query, nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := proto.Marshal(reply)
			if err != nil {
				t.Fatal(err)
			}
			decoded := &pb.AgentClientMessage{}
			if err := proto.Unmarshal(raw, decoded); err != nil {
				t.Fatal(err)
			}
			if response := decoded.GetInteractionResponse(); response == nil || response.Id != id {
				t.Fatalf("correlation ID %d changed: %v", id, decoded)
			}
		}
	}
}

func TestInteractionStreamingRefusalPreservesTextAndGenuineClientCall(t *testing.T) {
	handled := make(chan struct{})
	_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
		defer close(handled)
		readClient(t, request.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TextDelta{TextDelta: &pb.TextDeltaUpdate{Text: "Checking the registered client tools."}}}))
		query := interactionQueries()[1]
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: query}})
		assertInteractionRefusal(t, query, readClient(t, request.Body))
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 12, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_lookup", ProviderIdentifier: "cpa", ToolCallId: "real-streamed-call"}}}}})
	})
	req := testRequest()
	req.Metadata = map[string]any{"request_path": "/v1/chat/completions"}
	stream, err := e.ExecuteStream(context.Background(), req)
	if err != nil {
		<-handled
		t.Fatal(err)
	}
	var text string
	var calls []toolCall
	var terminal int
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Error(chunk.Err)
			continue
		}
		var body struct {
			Choices []struct {
				Delta struct {
					Content   string     `json:"content"`
					ToolCalls []toolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(chunk.Payload, &body); err != nil {
			t.Error(err)
			continue
		}
		for _, choice := range body.Choices {
			text += choice.Delta.Content
			calls = append(calls, choice.Delta.ToolCalls...)
			if choice.FinishReason == "tool_calls" {
				terminal++
			}
		}
	}
	<-handled
	if text != "Checking the registered client tools." || len(calls) != 1 || calls[0].ID != "real-streamed-call" || calls[0].Function.Name != "lookup" || terminal != 1 {
		t.Fatalf("interaction damaged streaming output: text=%q calls=%v terminal=%d", text, calls, terminal)
	}
}
