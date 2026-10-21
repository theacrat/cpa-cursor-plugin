package cursor

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func clientToolsRequest(t *testing.T) pluginapi.ExecutorRequest {
	t.Helper()
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":"read the file"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}},{"type":"function","function":{"name":"shell","parameters":{"type":"object"}}}]}`)
	return req
}

func TestToolPolicyListsOnlyAdvertisedClientNames(t *testing.T) {
	b, err := buildRequest(clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	run := b.message.GetRunRequest()
	rules := b.blobs.data[hex.EncodeToString(run.ConversationState.RootPromptMessagesJson[1])]
	for _, name := range []string{"mcp_cpa_read", "mcp_cpa_shell"} {
		if !bytes.Contains(rules, []byte(name)) {
			t.Fatalf("policy omits real client entry %s", name)
		}
	}
	context := run.Action.GetUserMessageAction().RequestContext
	if len(context.McpInstructions) != 1 || context.McpInstructions[0].ServerName != "cpa" || !strings.Contains(context.McpInstructions[0].Instructions, "mcp_cpa_read") {
		t.Fatal("MCP catalogue has no executable-client guidance")
	}
	if context.WebSearchEnabled == nil || *context.WebSearchEnabled {
		t.Fatal("native web-search support was not disabled")
	}
	if run.GetMcpFileSystemOptions() == nil || run.GetMcpFileSystemOptions().Enabled {
		t.Fatal("run-level native filesystem support was not disabled")
	}
	if run.GetSkillOptions() == nil || len(run.GetSkillOptions().GetSkillDescriptors()) != 0 {
		t.Fatal("run-level native skill catalogue was not disabled")
	}
}

func TestToolPolicyReportsRealMismatchesWithoutGuessing(t *testing.T) {
	b, err := buildRequest(clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	policy := clientToolPolicy(b.tools)
	for _, phrase := range []string{
		"Treat this list as authoritative for this request.",
		"If a tool call shows an actual name or schema mismatch, report it specifically once and stop.",
		"Do not guess or retry with a different name.",
		"Otherwise, call a matching tool directly when useful and continue from its actual result.",
	} {
		if !strings.Contains(policy, phrase) {
			t.Fatalf("tool policy omits direct-use guidance %q", phrase)
		}
	}
	for _, phrase := range []string{"Do not narrate", "Do not report", "Do not mention"} {
		if strings.Contains(policy, phrase) {
			t.Fatalf("tool policy suppresses a real issue with %q", phrase)
		}
	}
}

func TestToolNamesStayStableAcrossInitialAndLiveCatalogues(t *testing.T) {
	b, err := buildRequest(clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	run := b.message.GetRunRequest()
	initial := run.GetMcpTools().GetMcpTools()
	context := run.Action.GetUserMessageAction().RequestContext.Tools
	for _, catalogue := range [][]*pb.McpToolDefinition{initial, context} {
		if len(catalogue) != len(b.tools) {
			t.Fatalf("catalogue has %d tools, want %d", len(catalogue), len(b.tools))
		}
		for index, tool := range catalogue {
			want := "mcp_" + toolProvider + "_" + b.tools[index].Name
			if tool.Name != b.tools[index].Name || tool.ToolName != want {
				t.Errorf("catalogue tool = (%q, %q), want (%q, %q)", tool.Name, tool.ToolName, b.tools[index].Name, want)
			}
		}
	}
}

func TestToolFreePolicyDoesNotClaimRegisteredClientTools(t *testing.T) {
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":"summarise"}],"tool_choice":"none","tools":[{"type":"function","function":{"name":"read"}}]}`)
	b, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	rules := b.blobs.data[hex.EncodeToString(b.message.GetRunRequest().ConversationState.RootPromptMessagesJson[1])]
	if !bytes.Contains(rules, []byte("No client tools")) || bytes.Contains(rules, []byte("mcp_cpa_read")) {
		t.Fatal("tool-free request advertised unavailable tools")
	}
}

func TestNativeReadRefusalNamesRealClientAlternative(t *testing.T) {
	b, err := buildRequest(clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	reply, err := execReply(&pb.ExecServerMessage{Id: 19, ExecId: "native-read", Message: &pb.ExecServerMessage_ReadArgs{ReadArgs: &pb.ReadArgs{Path: "/private/never-read"}}}, b.tools)
	if err != nil {
		t.Fatal(err)
	}
	result := reply.GetExecClientMessage()
	if result == nil || result.Id != 19 || result.ExecId != "native-read" || result.GetReadResult().GetRejected() == nil {
		t.Fatal("native read was not a typed refusal")
	}
	reason := result.GetReadResult().GetRejected().Reason
	if !strings.Contains(reason, "mcp_cpa_read") || !strings.Contains(reason, "No operation") || !strings.Contains(reason, "schema") {
		t.Fatalf("native refusal lacks client recovery guidance: %s", reason)
	}
}

func TestUnknownClientToolReplyListsAdvertisedTools(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, req *http.Request) {
		readClient(t, req.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 1, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "not-registered"}}}}})
		missing := readClient(t, req.Body).GetExecClientMessage().GetMcpResult().GetToolNotFound()
		if missing == nil || len(missing.AvailableTools) != 2 || missing.AvailableTools[0] != "mcp_cpa_read" {
			t.Error("unknown client tool lost catalogue alternatives")
		}
		writeServer(t, w, endTurn())
	})
	if _, err := e.Execute(context.Background(), clientToolsRequest(t)); err != nil {
		t.Fatal(err)
	}
}

func TestProviderQualifiedClientAliasReturnsOriginalName(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, req *http.Request) {
		readClient(t, req.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 1, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{Name: "cpa-mcp_cpa_read", ProviderIdentifier: "cpa", ToolCallId: "client-read"}}}}})
	})
	response, err := e.Execute(context.Background(), clientToolsRequest(t))
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
	if len(body.Choices[0].Message.ToolCalls) != 1 || body.Choices[0].Message.ToolCalls[0].Function.Name != "read" {
		t.Fatalf("provider-qualified tool was not returned to its client: %s", response.Payload)
	}
}

func TestDisabledNativeRetriesStopWithoutForwardingOperations(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, req *http.Request) {
		readClient(t, req.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for id := uint32(1); id <= maxRejectedToolCalls; id++ {
			writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: id, Message: &pb.ExecServerMessage_ReadArgs{ReadArgs: &pb.ReadArgs{Path: "/private/never-read"}}}}})
			if readClient(t, req.Body).GetExecClientMessage().GetReadResult().GetRejected() == nil {
				t.Error("native retry was not rejected")
			}
		}
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 99, Message: &pb.ExecServerMessage_ReadArgs{ReadArgs: &pb.ReadArgs{Path: "/private/never-read"}}}}})
	})
	_, err := e.Execute(context.Background(), clientToolsRequest(t))
	if err == nil || !strings.Contains(err.Error(), "repeatedly requested disabled native tools") {
		t.Fatalf("unbounded native retries: %v", err)
	}
}

func TestMCPStateCatalogueIsRefused(t *testing.T) {
	b, err := buildRequest(clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	reply, err := execReply(&pb.ExecServerMessage{Id: 9, Message: &pb.ExecServerMessage_McpStateExecArgs{McpStateExecArgs: &pb.McpStateExecArgs{ServerIdentifiers: []string{"custom-user-tools"}}}}, b.tools)
	if err != nil {
		t.Fatal(err)
	}
	result := reply.GetExecClientMessage().GetMcpStateExecResult()
	if result.GetSuccess() != nil {
		t.Fatal("MCP state discovery returned a success result")
	}
	if reason := result.GetRejected().GetReason(); !strings.Contains(reason, "dynamic MCP discovery is disabled") || !strings.Contains(reason, "cpa client tools") {
		t.Fatalf("MCP state refusal lacks client-tool guidance: %s", reason)
	}
}

func TestRepeatedMCPStateRequestsStopWithoutNativeFallback(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, req *http.Request) {
		readClient(t, req.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for id := uint32(1); id <= maxRejectedToolCalls+1; id++ {
			writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: id, Message: &pb.ExecServerMessage_McpStateExecArgs{McpStateExecArgs: &pb.McpStateExecArgs{}}}}})
			if id <= maxRejectedToolCalls && readClient(t, req.Body).GetExecClientMessage().GetMcpStateExecResult().GetRejected() == nil {
				t.Error("dynamic MCP discovery was not refused")
			}
		}
	})
	_, err := e.Execute(context.Background(), clientToolsRequest(t))
	if err == nil || !strings.Contains(err.Error(), "repeatedly requested disabled dynamic MCP discovery") {
		t.Fatalf("unbounded dynamic MCP discovery: %v", err)
	}
}

func TestNativeRefusalCanRecoverThroughRegisteredClientRead(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, req *http.Request) {
		readClient(t, req.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 1, Message: &pb.ExecServerMessage_ReadArgs{ReadArgs: &pb.ReadArgs{Path: "/client/file"}}}}})
		if readClient(t, req.Body).GetExecClientMessage().GetReadResult().GetRejected() == nil {
			t.Error("native read did not fail truthfully")
		}
		value, _ := structpb.NewValue("/client/file")
		raw, _ := proto.Marshal(value)
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 2, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_read", ProviderIdentifier: "cpa", ToolCallId: "client-read", Args: map[string][]byte{"path": raw}}}}}})
	})
	response, err := e.Execute(context.Background(), clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response.Payload, []byte(`"name":"read"`)) || !bytes.Contains(response.Payload, []byte(`/client/file`)) {
		t.Fatalf("recovery did not preserve client tool or arguments: %s", response.Payload)
	}
}

func TestNativeExecTypesNeverClaimSuccess(t *testing.T) {
	b, err := buildRequest(clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	requests := []*pb.ExecServerMessage{
		{Message: &pb.ExecServerMessage_WriteArgs{WriteArgs: &pb.WriteArgs{Path: "/private/no-write"}}},
		{Message: &pb.ExecServerMessage_DeleteArgs{DeleteArgs: &pb.DeleteArgs{Path: "/private/no-delete"}}},
		{Message: &pb.ExecServerMessage_LsArgs{LsArgs: &pb.LsArgs{Path: "/private/no-list"}}},
		{Message: &pb.ExecServerMessage_GrepArgs{GrepArgs: &pb.GrepArgs{Pattern: "no-search"}}},
		{Message: &pb.ExecServerMessage_ShellArgs{ShellArgs: &pb.ShellArgs{Command: "no-execute"}}},
		{Message: &pb.ExecServerMessage_ShellStreamArgs{ShellStreamArgs: &pb.ShellArgs{Command: "no-stream"}}},
		{Message: &pb.ExecServerMessage_BackgroundShellSpawnArgs{BackgroundShellSpawnArgs: &pb.BackgroundShellSpawnArgs{Command: "no-spawn"}}},
		{Message: &pb.ExecServerMessage_SubagentArgs{SubagentArgs: &pb.SubagentArgs{}}},
	}
	for _, req := range requests {
		req.Id, req.ExecId = 33, "refused"
		reply, err := execReply(req, b.tools)
		if err != nil {
			t.Fatal(err)
		}
		message := reply.GetExecClientMessage()
		if message == nil || message.Id != 33 || message.ExecId != "refused" || strings.Contains(message.String(), "success:") || !strings.Contains(message.String(), "No operation was performed") {
			t.Fatalf("untruthful native result: %v", reply)
		}
	}
}

func TestWrongMCPProviderCannotInvokeRegisteredClientTool(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, req *http.Request) {
		readClient(t, req.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{Id: 1, Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "mcp_cpa_read", ProviderIdentifier: "another-server"}}}}})
		if readClient(t, req.Body).GetExecClientMessage().GetMcpResult().GetToolNotFound() == nil {
			t.Error("foreign MCP namespace was not rejected")
		}
		writeServer(t, w, endTurn())
	})
	response, err := e.Execute(context.Background(), clientToolsRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(response.Payload, []byte(`"tool_calls"`)) {
		t.Fatal("foreign MCP namespace reached client execution")
	}
}
