package cursor

import (
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

const rejectionDiagnosticSecret = "private-diagnostic-sentinel"

func rejectionDiagnosticMCP(provider, tool, name string, args map[string][]byte) *pb.AgentServerMessage {
	return &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 123, ExecId: rejectionDiagnosticSecret, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: tool, Name: name, ProviderIdentifier: provider, ToolCallId: rejectionDiagnosticSecret, Args: args}}}}}
}

func diagnosticEvents(t *testing.T, lines []string) []map[string]json.RawMessage {
	t.Helper()
	var events []map[string]json.RawMessage
	for _, line := range lines {
		if strings.Contains(line, rejectionDiagnosticSecret) || strings.Contains(line, "https://") || strings.Contains(line, "mcp_cpa_read") || strings.Contains(line, "mcp_cpa_shell") {
			t.Fatal("diagnostics exposed upstream names or payload data")
		}
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		for key := range event {
			switch key {
			case "event", "run_id", "category", "subtype", "field_number", "provider", "resolution", "argument_shape", "count", "limit", "outcome":
			default:
				t.Errorf("unbounded or unexpected diagnostic field %q", key)
			}
		}
		if len(event) != 11 {
			t.Errorf("diagnostic fields = %d, want 11", len(event))
		}
		var correlation string
		_ = json.Unmarshal(event["run_id"], &correlation)
		if !strings.HasPrefix(correlation, "chatcmpl-") || len(correlation) != len("chatcmpl-")+36 {
			t.Errorf("diagnostic correlation is not a locally generated run ID: %q", correlation)
		}
		events = append(events, event)
	}
	return events
}

func TestToolRejectionDiagnosticsClassifyWithoutPayloads(t *testing.T) {
	value, _ := structpb.NewValue(map[string]any{rejectionDiagnosticSecret: "https://example.invalid/" + rejectionDiagnosticSecret})
	raw, _ := proto.Marshal(value)
	unknownNative := &pb.ExecServerMessage{Id: 123, ExecId: rejectionDiagnosticSecret}
	unknownNative.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), []byte(rejectionDiagnosticSecret)))
	unknownInteraction := &pb.InteractionQuery{Id: 456}
	unknownInteraction.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), []byte(rejectionDiagnosticSecret)))
	webFetch := &pb.InteractionQuery{Id: 789}
	webFetch.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 9, protowire.BytesType), []byte(rejectionDiagnosticSecret)))
	for _, test := range []struct {
		name, category, subtype, provider, resolution, shape, outcome string
		field                                                         int
		message                                                       *pb.AgentServerMessage
		fails                                                         bool
	}{
		{"native read", "native", "read_args", "none", "not_applicable", "not_applicable", "refused", 7, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 123, ExecId: rejectionDiagnosticSecret, Message: &pb.ExecServerMessage_ReadArgs{ReadArgs: &pb.ReadArgs{Path: rejectionDiagnosticSecret}}}}}, false},
		{"unknown native", "native", "unknown", "none", "not_applicable", "not_applicable", "refused", 0, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: unknownNative}}, false},
		{"foreign recognised wrapper", "mcp", "mcp_args", "foreign", "recognised", "dynamic_wrapper", "refused", 11, rejectionDiagnosticMCP(rejectionDiagnosticSecret, "mcp_cpa_read", "", map[string][]byte{"mcpDetails": raw, rejectionDiagnosticSecret: raw}), false},
		{"empty provider missing flat", "mcp", "mcp_args", "empty", "missing", "flat", "refused", 11, rejectionDiagnosticMCP("", rejectionDiagnosticSecret, "", map[string][]byte{rejectionDiagnosticSecret: raw}), false},
		{"cpa provider missing empty", "mcp", "mcp_args", "cpa", "missing", "empty", "refused", 11, rejectionDiagnosticMCP("cpa", rejectionDiagnosticSecret, "", nil), false},
		{"foreign conflicting names", "mcp", "mcp_args", "foreign", "conflicting", "empty", "refused", 11, rejectionDiagnosticMCP(rejectionDiagnosticSecret, "mcp_cpa_read", "mcp_cpa_shell", nil), false},
		{"typed interaction", "interaction", "web_search_request_query", "none", "not_applicable", "not_applicable", "refused", 2, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: &pb.InteractionQuery{Id: 456, Query: &pb.InteractionQuery_WebSearchRequestQuery{WebSearchRequestQuery: &pb.WebSearchRequestQuery{Args: &pb.WebSearchArgs{SearchTerm: rejectionDiagnosticSecret}}}}}}, false},
		{"web fetch interaction", "interaction", "web_fetch_field_9", "none", "not_applicable", "not_applicable", "refused", 9, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: webFetch}}, false},
		{"VM setup unsupported", "interaction", "setup_vm_environment_args", "none", "not_applicable", "not_applicable", "unsupported", 8, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: &pb.InteractionQuery{Id: 456, Query: &pb.InteractionQuery_SetupVmEnvironmentArgs{SetupVmEnvironmentArgs: &pb.SetupVmEnvironmentArgs{InstallCommand: rejectionDiagnosticSecret}}}}}, true},
		{"unknown interaction unsupported", "interaction", "unknown", "none", "not_applicable", "not_applicable", "unsupported", 0, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: unknownInteraction}}, true},
		{"empty interaction unsupported", "interaction", "empty", "none", "not_applicable", "not_applicable", "unsupported", 0, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: &pb.InteractionQuery{Id: 456}}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handled := make(chan struct{})
			_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
				defer close(handled)
				readClient(t, request.Body)
				w.Header().Set("Content-Type", "application/connect+proto")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				writeServer(t, w, test.message)
				if test.fails {
					_, _, _ = readFrame(request.Body, "")
					return
				}
				readClient(t, request.Body)
				writeServer(t, w, endTurn())
			})
			var lines []string
			e.Log = func(line string) { lines = append(lines, line) }
			_, err := e.Execute(context.Background(), clientToolsRequest(t))
			<-handled
			if (err != nil) != test.fails {
				t.Fatalf("diagnostics changed rejection behaviour: %v", err)
			}
			events := diagnosticEvents(t, lines)
			if len(events) != 1 {
				t.Fatalf("rejection events = %d, want 1", len(events))
			}
			for key, want := range map[string]string{"event": "cursor_tool_rejected", "category": test.category, "subtype": test.subtype, "provider": test.provider, "resolution": test.resolution, "argument_shape": test.shape, "outcome": test.outcome} {
				var got string
				_ = json.Unmarshal(events[0][key], &got)
				if got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			for key, want := range map[string]int{"field_number": test.field, "count": 1, "limit": maxRejectedToolCalls} {
				var got int
				_ = json.Unmarshal(events[0][key], &got)
				if got != want {
					t.Errorf("%s = %d, want %d", key, got, want)
				}
			}
		})
	}
}

func TestToolRejectionDiagnosticsStayBoundedAcrossCategories(t *testing.T) {
	handled := make(chan struct{})
	_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
		defer close(handled)
		readClient(t, request.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for i := 1; i <= maxRejectedToolCalls+1; i++ {
			var message *pb.AgentServerMessage
			switch i % 3 {
			case 0:
				message = rejectionDiagnosticMCP(rejectionDiagnosticSecret, "mcp_cpa_read", "", nil)
			case 1:
				message = &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: uint32(i), Message: &pb.ExecServerMessage_ReadArgs{ReadArgs: &pb.ReadArgs{Path: rejectionDiagnosticSecret}}}}}
			default:
				message = &pb.AgentServerMessage{Message: &pb.AgentServerMessage_InteractionQuery{InteractionQuery: &pb.InteractionQuery{Id: uint32(i), Query: &pb.InteractionQuery_WebSearchRequestQuery{WebSearchRequestQuery: &pb.WebSearchRequestQuery{}}}}}
			}
			writeServer(t, w, message)
			if i <= maxRejectedToolCalls {
				readClient(t, request.Body)
			}
		}
	})
	var lines []string
	e.Log = func(line string) { lines = append(lines, line) }
	_, err := e.Execute(context.Background(), clientToolsRequest(t))
	<-handled
	if err == nil || !strings.Contains(err.Error(), "repeatedly requested") {
		t.Fatalf("rejection bound changed: %v", err)
	}
	events := diagnosticEvents(t, lines)
	if len(events) != maxRejectedToolCalls+1 {
		t.Fatalf("bounded rejection events = %d, want %d", len(events), maxRejectedToolCalls+1)
	}
	for index, event := range events {
		if string(event["count"]) != fmt.Sprint(index+1) || string(event["run_id"]) != string(events[0]["run_id"]) {
			t.Error("rejection count or run correlation changed")
		}
		want := `"refused"`
		if index == maxRejectedToolCalls {
			want = `"limit"`
		}
		if string(event["outcome"]) != want {
			t.Errorf("outcome = %s, want %s", event["outcome"], want)
		}
	}
}

func TestToolRejectionDiagnosticsDoNotChangeAcceptedNamePrecedence(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
		readClient(t, request.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, rejectionDiagnosticMCP("cpa", "mcp_cpa_read", "mcp_cpa_shell", nil))
	})
	var lines []string
	e.Log = func(line string) { lines = append(lines, line) }
	response, err := e.Execute(context.Background(), clientToolsRequest(t))
	if err != nil || !strings.Contains(string(response.Payload), `"name":"read"`) {
		t.Fatalf("diagnostics changed tool-name precedence: %v", err)
	}
	if len(lines) != 1 || len(diagnosticEvents(t, lines)) != 0 {
		t.Fatal("accepted MCP call emitted rejection diagnostics")
	}
}

func TestToolRejectionDiagnosticsAllowNilLogger(t *testing.T) {
	handled := make(chan struct{})
	_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
		defer close(handled)
		readClient(t, request.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, rejectionDiagnosticMCP("", rejectionDiagnosticSecret, "", nil))
		readClient(t, request.Body)
		writeServer(t, w, endTurn())
	})
	e.Log = nil
	_, err := e.Execute(context.Background(), clientToolsRequest(t))
	<-handled
	if err != nil {
		t.Fatal(err)
	}
}

func TestToolRejectionDiagnosticsIgnoreCatalogueRequests(t *testing.T) {
	handled := make(chan struct{})
	_, e := tlsFixture(t, func(w http.ResponseWriter, request *http.Request) {
		defer close(handled)
		readClient(t, request.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for _, message := range []*pb.ExecServerMessage{
			{Id: 1, Message: &pb.ExecServerMessage_RequestContextArgs{RequestContextArgs: &pb.RequestContextArgs{}}},
			{Id: 2, Message: &pb.ExecServerMessage_McpStateExecArgs{McpStateExecArgs: &pb.McpStateExecArgs{}}},
		} {
			writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: message}})
			readClient(t, request.Body)
		}
		writeServer(t, w, endTurn())
	})
	var lines []string
	e.Log = func(line string) { lines = append(lines, line) }
	_, err := e.Execute(context.Background(), clientToolsRequest(t))
	<-handled
	if err != nil || len(lines) != 1 || len(diagnosticEvents(t, lines)) != 0 {
		t.Fatalf("catalogue requests were logged as rejections: %v", err)
	}
}

func TestToolRejectionDiagnosticsDropCountsBeyondTerminalBound(t *testing.T) {
	b, err := buildRequest(clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	var lines []string
	e.Log = func(line string) { lines = append(lines, line) }
	request := &pb.ExecServerMessage{Message: &pb.ExecServerMessage_ReadArgs{ReadArgs: &pb.ReadArgs{Path: rejectionDiagnosticSecret}}}
	for count := 1; count <= 100; count++ {
		e.logToolRejection(b, request, count, "refused")
	}
	if len(diagnosticEvents(t, lines)) != maxRejectedToolCalls+1 {
		t.Fatal("diagnostics exceeded the existing terminal refusal bound")
	}
}
