package cursor

import (
	"strings"

	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
)

const maxRejectedToolCalls = 8

func clientToolQualifiedName(name string) string {
	return "mcp_" + toolProvider + "_" + name
}

func clientToolAliases(wire string) []string {
	qualified := clientToolQualifiedName(wire)
	return []string{wire, qualified, toolProvider + "-" + wire, toolProvider + "-" + qualified}
}

func advertisedClientTools(tools []*pb.McpToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, clientToolQualifiedName(tool.Name))
	}
	return names
}

func clientToolPolicy(tools []*pb.McpToolDefinition) string {
	policy := "Native Cursor filesystem, shell, computer, network and subagent operations are disabled. Do not call or retry native operations. "
	if len(tools) == 0 {
		return policy + "No client tools are available for this request. Answer using the supplied conversation only."
	}
	return policy + "Available client tools for this request: " + strings.Join(advertisedClientTools(tools), ", ") + ". Treat this list as authoritative for this request. If a tool call shows an actual name or schema mismatch, report it specifically once and stop. Do not guess or retry with a different name. Otherwise, call a matching tool directly when useful and continue from its actual result."
}

func clientRequestContext(tools []*pb.McpToolDefinition) *pb.RequestContext {
	policy := clientToolPolicy(tools)
	return &pb.RequestContext{
		Rules: []*pb.CursorRule{{FullPath: ".cursorrules", Content: policy, Type: &pb.CursorRuleType{Type: &pb.CursorRuleType_Global{Global: &pb.CursorRuleTypeGlobal{}}}}},
		Env:   &pb.RequestContextEnv{}, Tools: tools,
		McpInstructions:      []*pb.McpInstructions{{ServerName: toolProvider, Instructions: policy}},
		WebSearchEnabled:     proto.Bool(false),
		McpFileSystemOptions: &pb.McpFileSystemOptions{Enabled: false},
		SkillOptions:         &pb.SkillOptions{},
	}
}

func nativeToolRefusal(tools []*pb.McpToolDefinition) string {
	reason := "This native Cursor operation is disabled. No operation was performed. Do not retry this native tool. "
	if len(tools) == 0 {
		return reason + "No client tools are exposed for this request."
	}
	return reason + "The registered client tools are still available through the cpa MCP server: " + strings.Join(advertisedClientTools(tools), ", ") + ". Choose the appropriate registered entry and construct its arguments from that tool's schema."
}

func refusedNativeExec(exec *pb.ExecServerMessage, tools []*pb.McpToolDefinition) *pb.AgentClientMessage {
	reply := &pb.ExecClientMessage{Id: exec.Id, ExecId: exec.ExecId}
	reason := nativeToolRefusal(tools)
	switch request := exec.Message.(type) {
	case *pb.ExecServerMessage_ReadArgs:
		reply.Message = &pb.ExecClientMessage_ReadResult{ReadResult: &pb.ReadResult{Result: &pb.ReadResult_Rejected{Rejected: &pb.ReadRejected{Path: request.ReadArgs.Path, Reason: reason}}}}
	case *pb.ExecServerMessage_WriteArgs:
		reply.Message = &pb.ExecClientMessage_WriteResult{WriteResult: &pb.WriteResult{Result: &pb.WriteResult_Rejected{Rejected: &pb.WriteRejected{Path: request.WriteArgs.Path, Reason: reason}}}}
	case *pb.ExecServerMessage_DeleteArgs:
		reply.Message = &pb.ExecClientMessage_DeleteResult{DeleteResult: &pb.DeleteResult{Result: &pb.DeleteResult_Rejected{Rejected: &pb.DeleteRejected{Path: request.DeleteArgs.Path, Reason: reason}}}}
	case *pb.ExecServerMessage_LsArgs:
		reply.Message = &pb.ExecClientMessage_LsResult{LsResult: &pb.LsResult{Result: &pb.LsResult_Rejected{Rejected: &pb.LsRejected{Path: request.LsArgs.Path, Reason: reason}}}}
	case *pb.ExecServerMessage_GrepArgs:
		reply.Message = &pb.ExecClientMessage_GrepResult{GrepResult: &pb.GrepResult{Result: &pb.GrepResult_Error{Error: &pb.GrepError{Error: reason}}}}
	case *pb.ExecServerMessage_ShellArgs:
		reply.Message = &pb.ExecClientMessage_ShellResult{ShellResult: &pb.ShellResult{Result: &pb.ShellResult_Rejected{Rejected: &pb.ShellRejected{Command: request.ShellArgs.Command, WorkingDirectory: request.ShellArgs.WorkingDirectory, Reason: reason}}}}
	case *pb.ExecServerMessage_ShellStreamArgs:
		reply.Message = &pb.ExecClientMessage_ShellStream{ShellStream: &pb.ShellStream{Event: &pb.ShellStream_Rejected{Rejected: &pb.ShellRejected{Command: request.ShellStreamArgs.Command, WorkingDirectory: request.ShellStreamArgs.WorkingDirectory, Reason: reason}}}}
	case *pb.ExecServerMessage_BackgroundShellSpawnArgs:
		reply.Message = &pb.ExecClientMessage_BackgroundShellSpawnResult{BackgroundShellSpawnResult: &pb.BackgroundShellSpawnResult{Result: &pb.BackgroundShellSpawnResult_Rejected{Rejected: &pb.ShellRejected{Command: request.BackgroundShellSpawnArgs.Command, WorkingDirectory: request.BackgroundShellSpawnArgs.WorkingDirectory, Reason: reason}}}}
	case *pb.ExecServerMessage_SubagentArgs:
		reply.Message = &pb.ExecClientMessage_SubagentResult{SubagentResult: &pb.SubagentResult{Result: &pb.SubagentResult_Error{Error: &pb.SubagentError{Error: reason}}}}
	default:
		return &pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientControlMessage{ExecClientControlMessage: &pb.ExecClientControlMessage{Message: &pb.ExecClientControlMessage_Throw{Throw: &pb.ExecClientThrow{Id: exec.Id, Error: reason}}}}}
	}
	return &pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientMessage{ExecClientMessage: reply}}
}
