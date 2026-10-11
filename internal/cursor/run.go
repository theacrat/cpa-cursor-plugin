package cursor

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type runResult struct {
	text, thinking string
	calls          []toolCall
	output, total  int
}

func (r runResult) usage() map[string]int {
	input := r.total - r.output
	if input < 0 {
		input = 0
	}
	return map[string]int{"prompt_tokens": input, "completion_tokens": r.output, "total_tokens": input + r.output}
}

func (e *Executor) run(ctx context.Context, req pluginapi.ExecutorRequest, b *builtRequest, onText func(string, bool) error) (runResult, error) {
	// Cancellation closes the host pump before draining it. The child context
	// also ensures early tool-call completion cancels the host's live stream.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var t *runTransport
	var err error
	if req.HTTPClient != nil {
		t, err = e.hostRun(ctx, req, b)
	} else if e.client != nil {
		t, err = e.nativeRun(ctx, req, b)
	} else {
		return runResult{}, errors.New("Cursor inference requires the CPA host HTTP client")
	}
	if err != nil {
		return runResult{}, err
	}
	defer func() { cancel(); t.close() }()
	r := runResult{}
	seen := map[string]bool{}
	reader := t.reader.(frameReader)
	for {
		flags, raw, err := readFrame(reader.Reader, reader.encoding)
		if err != nil {
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return r, errors.New("Cursor stream ended before turnEnded")
			}
			return r, err
		}
		if flags&2 != 0 {
			var trailer struct {
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &trailer) != nil {
				return r, errors.New("invalid Cursor Connect trailer")
			}
			if trailer.Error != nil {
				return r, connectStatus(trailer.Error.Code)
			}
			return r, errors.New("Cursor Connect stream ended before turnEnded")
		}
		msg := &pb.AgentServerMessage{}
		if proto.Unmarshal(raw, msg) != nil {
			return r, errors.New("invalid Cursor server protobuf")
		}
		switch m := msg.Message.(type) {
		case *pb.AgentServerMessage_InteractionUpdate:
			switch u := m.InteractionUpdate.Message.(type) {
			case *pb.InteractionUpdate_TextDelta:
				text := u.TextDelta.Text
				if len(r.text)+len(text) > maxFrame {
					return r, errors.New("Cursor output exceeds limit")
				}
				r.text += text
				if onText != nil && text != "" {
					if err := onText(text, false); err != nil {
						return r, err
					}
				}
			case *pb.InteractionUpdate_ThinkingDelta:
				text := u.ThinkingDelta.Text
				if len(r.thinking)+len(text) > maxFrame {
					return r, errors.New("Cursor thinking output exceeds limit")
				}
				r.thinking += text
				if onText != nil && text != "" {
					if err := onText(text, true); err != nil {
						return r, err
					}
				}
			case *pb.InteractionUpdate_TokenDelta:
				if u.TokenDelta.Tokens < 0 {
					return r, errors.New("invalid Cursor token delta")
				}
				r.output += int(u.TokenDelta.Tokens)
			case *pb.InteractionUpdate_TurnEnded:
				return r, nil
			}
		case *pb.AgentServerMessage_ConversationCheckpointUpdate:
			if detail := m.ConversationCheckpointUpdate.TokenDetails; detail != nil {
				r.total = int(detail.UsedTokens)
			}
		case *pb.AgentServerMessage_KvServerMessage:
			kv := m.KvServerMessage
			reply := &pb.KvClientMessage{Id: kv.Id}
			switch k := kv.Message.(type) {
			case *pb.KvServerMessage_GetBlobArgs:
				data, ok := b.blobs.data[hex.EncodeToString(k.GetBlobArgs.BlobId)]
				if !ok {
					return r, errors.New("Cursor requested a missing conversation blob; refusing incomplete history")
				}
				reply.Message = &pb.KvClientMessage_GetBlobResult{GetBlobResult: &pb.GetBlobResult{BlobData: data}}
			case *pb.KvServerMessage_SetBlobArgs:
				if err := b.blobs.set(k.SetBlobArgs.BlobId, k.SetBlobArgs.BlobData); err != nil {
					return r, err
				}
				reply.Message = &pb.KvClientMessage_SetBlobResult{SetBlobResult: &pb.SetBlobResult{}}
			default:
				return r, errors.New("unsupported Cursor KV request")
			}
			if err := t.send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_KvClientMessage{KvClientMessage: reply}}); err != nil {
				return r, err
			}
		case *pb.AgentServerMessage_ExecServerMessage:
			exec := m.ExecServerMessage
			if args := exec.GetMcpArgs(); args != nil {
				name := b.resolve[args.ToolName]
				if name == "" {
					name = b.resolve[args.Name]
				}
				available := false
				for _, tool := range b.tools {
					if b.wire[name] == tool.Name && name != "" {
						available = true
						break
					}
				}
				if !available {
					result := &pb.ExecClientMessage{Id: exec.Id, ExecId: exec.ExecId, Message: &pb.ExecClientMessage_McpResult{McpResult: &pb.McpResult{Result: &pb.McpResult_ToolNotFound{ToolNotFound: &pb.McpToolNotFound{Name: args.ToolName}}}}}
					if err := t.send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientMessage{ExecClientMessage: result}}); err != nil {
						return r, err
					}
					continue
				}
				decoded := map[string]any{}
				for key, value := range args.Args {
					v := &structpb.Value{}
					if err := proto.Unmarshal(value, v); err == nil && v.Kind != nil {
						decoded[key] = v.AsInterface()
					} else {
						decoded[key] = string(value)
					}
				}
				rawArgs, _ := json.Marshal(decoded)
				id := args.ToolCallId
				if id == "" {
					id = "call_" + uuid()
				}
				if !seen[id] {
					r.calls = append(r.calls, toolCall{ID: id, Type: "function", Function: functionCall{Name: name, Arguments: string(rawArgs)}})
					seen[id] = true
				}
				// OpenAI callers must execute the tool outside this plugin. Stop this run
				// now; the next call's full transcript carries the result into ResumeAction.
				return r, nil
			}
			reply, err := execReply(exec, b.tools)
			if err != nil {
				return r, err
			}
			if err := t.send(reply); err != nil {
				return r, err
			}
		case *pb.AgentServerMessage_InteractionQuery:
			// These require user approval or an interactive UI not represented by
			// chat-completions. Fail rather than invent consent or execute a network tool.
			return r, errors.New("Cursor interaction query requires an unsupported interactive approval flow")
		case *pb.AgentServerMessage_ExecServerControlMessage:
			// Abort notices relate to an individual exec, not necessarily this turn.
		default:
			return r, errors.New("unsupported Cursor server message; protocol schema may have changed")
		}
	}
}
func execReply(exec *pb.ExecServerMessage, tools []*pb.McpToolDefinition) (*pb.AgentClientMessage, error) {
	reply := &pb.ExecClientMessage{Id: exec.Id, ExecId: exec.ExecId}
	switch exec.Message.(type) {
	case *pb.ExecServerMessage_RequestContextArgs:
		reply.Message = &pb.ExecClientMessage_RequestContextResult{RequestContextResult: &pb.RequestContextResult{Result: &pb.RequestContextResult_Success{Success: &pb.RequestContextSuccess{RequestContext: &pb.RequestContext{Env: &pb.RequestContextEnv{}, Tools: tools}}}}}
	case *pb.ExecServerMessage_McpStateExecArgs:
		advertised := make([]*pb.McpToolDefinition, 0, len(tools))
		for _, tool := range tools {
			copy := proto.Clone(tool).(*pb.McpToolDefinition)
			copy.ToolName = "mcp_" + toolProvider + "_" + tool.Name
			advertised = append(advertised, copy)
		}
		servers := []*pb.McpStateServer{}
		if len(tools) > 0 {
			servers = append(servers, &pb.McpStateServer{ServerName: toolProvider, ServerIdentifier: toolProvider, Tools: advertised})
		}
		reply.Message = &pb.ExecClientMessage_McpStateExecResult{McpStateExecResult: &pb.McpStateExecResult{Result: &pb.McpStateExecResult_Success{Success: &pb.McpStateSuccess{Servers: servers}}}}
	default:
		// The generic throw is explicitly a failed exec, never a fabricated success.
		// It covers known native operations and unknown protocol variants safely.
		reason := "Native local filesystem, shell, computer, network and subagent operations are unavailable. Use the registered MCP client tools instead."
		return &pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientControlMessage{ExecClientControlMessage: &pb.ExecClientControlMessage{Message: &pb.ExecClientControlMessage_Throw{Throw: &pb.ExecClientThrow{Id: exec.Id, Error: reason}}}}}, nil
	}
	return &pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientMessage{ExecClientMessage: reply}}, nil
}
