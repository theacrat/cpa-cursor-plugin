package cursor

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

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

// runTiming records per-phase run durations for diagnosing slow or stuck
// upstream turns. It carries byte counts and variant IDs only.
type runTiming struct {
	start     time.Time
	open      time.Duration
	firstByte time.Duration
	table     time.Duration
	inBytes   int
	outBytes  int
	failed    bool
}

func (e *Executor) logTiming(model string, timing runTiming) {
	if e.Log == nil {
		return
	}
	e.Log("run model=" + model +
		" table_ms=" + strconv.FormatInt(timing.table.Milliseconds(), 10) +
		" open_ms=" + strconv.FormatInt(timing.open.Milliseconds(), 10) +
		" first_ms=" + strconv.FormatInt(timing.firstByte.Milliseconds(), 10) +
		" total_ms=" + strconv.FormatInt(time.Since(timing.start).Milliseconds(), 10) +
		" in=" + strconv.Itoa(timing.inBytes) +
		" out=" + strconv.Itoa(timing.outBytes) +
		" failed=" + strconv.FormatBool(timing.failed))
}

func (r runResult) usage() map[string]int {
	input := r.total - r.output
	if input < 0 {
		input = 0
	}
	return map[string]int{"prompt_tokens": input, "completion_tokens": r.output, "total_tokens": input + r.output}
}

func (e *Executor) run(ctx context.Context, req pluginapi.ExecutorRequest, b *builtRequest, onText func(string, bool) error, tableTook time.Duration) (runResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timing := runTiming{start: time.Now(), table: tableTook, inBytes: len(req.Payload)}
	done := func(result runResult, err error) (runResult, error) {
		timing.outBytes = len(result.text) + len(result.thinking)
		timing.failed = err != nil
		e.logTiming(b.model, timing)
		return result, err
	}
	var t *runTransport
	var err error
	if req.HTTPClient != nil {
		t, err = e.hostRun(ctx, req, b)
	} else if e.client != nil {
		t, err = e.nativeRun(ctx, req, b)
	} else {
		return done(runResult{}, errors.New("Cursor inference requires the CPA host HTTP client"))
	}
	if err != nil {
		return done(runResult{}, err)
	}
	timing.open = time.Since(timing.start)
	defer func() { cancel(); t.close() }()
	r := runResult{}
	seen := map[string]bool{}
	rejectedTools := 0
	reader := t.reader.(frameReader)
	firstFrame := true
	for {
		flags, raw, err := readFrame(reader.Reader, reader.encoding)
		if firstFrame && err == nil {
			firstFrame = false
			timing.firstByte = time.Since(timing.start)
		}
		if err != nil {
			if ctx.Err() != nil {
				return done(r, ctx.Err())
			}
			if errors.Is(err, io.EOF) {
				return done(r, errors.New("Cursor stream ended before turnEnded"))
			}
			return done(r, err)
		}
		if flags&2 != 0 {
			var trailer struct {
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &trailer) != nil {
				return done(r, errors.New("invalid Cursor Connect trailer"))
			}
			if trailer.Error != nil {
				return done(r, connectStatus(trailer.Error.Code))
			}
			return done(r, errors.New("Cursor Connect stream ended before turnEnded"))
		}
		msg := &pb.AgentServerMessage{}
		if proto.Unmarshal(raw, msg) != nil {
			return done(r, errors.New("invalid Cursor server protobuf"))
		}
		switch m := msg.Message.(type) {
		case *pb.AgentServerMessage_InteractionUpdate:
			switch u := m.InteractionUpdate.Message.(type) {
			case *pb.InteractionUpdate_TextDelta:
				text := u.TextDelta.Text
				if len(r.text)+len(text) > maxFrame {
					return done(r, errors.New("Cursor output exceeds limit"))
				}
				r.text += text
				if onText != nil && text != "" {
					if err := onText(text, false); err != nil {
						return done(r, err)
					}
				}
			case *pb.InteractionUpdate_ThinkingDelta:
				text := u.ThinkingDelta.Text
				if len(r.thinking)+len(text) > maxFrame {
					return done(r, errors.New("Cursor thinking output exceeds limit"))
				}
				r.thinking += text
				if onText != nil && text != "" {
					if err := onText(text, true); err != nil {
						return done(r, err)
					}
				}
			case *pb.InteractionUpdate_TokenDelta:
				if u.TokenDelta.Tokens < 0 {
					return done(r, errors.New("invalid Cursor token delta"))
				}
				r.output += int(u.TokenDelta.Tokens)
			case *pb.InteractionUpdate_TurnEnded:
				return done(r, nil)
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
					return done(r, errors.New("Cursor requested a missing conversation blob; refusing incomplete history"))
				}
				reply.Message = &pb.KvClientMessage_GetBlobResult{GetBlobResult: &pb.GetBlobResult{BlobData: data}}
			case *pb.KvServerMessage_SetBlobArgs:
				if err := b.blobs.set(k.SetBlobArgs.BlobId, k.SetBlobArgs.BlobData); err != nil {
					return done(r, err)
				}
				reply.Message = &pb.KvClientMessage_SetBlobResult{SetBlobResult: &pb.SetBlobResult{}}
			default:
				return done(r, errors.New("unsupported Cursor KV request"))
			}
			if err := t.send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_KvClientMessage{KvClientMessage: reply}}); err != nil {
				return done(r, err)
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
				if !available || args.ProviderIdentifier != "" && args.ProviderIdentifier != toolProvider {
					rejectedTools++
					if rejectedTools > maxRejectedToolCalls {
						return done(r, errors.New("Cursor repeatedly requested unavailable tools; no client operation was performed"))
					}
					result := &pb.ExecClientMessage{Id: exec.Id, ExecId: exec.ExecId, Message: &pb.ExecClientMessage_McpResult{McpResult: &pb.McpResult{Result: &pb.McpResult_ToolNotFound{ToolNotFound: &pb.McpToolNotFound{Name: first(args.ToolName, args.Name), AvailableTools: advertisedClientTools(b.tools)}}}}}
					if err := t.send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientMessage{ExecClientMessage: result}}); err != nil {
						return done(r, err)
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
				id := clientToolCallID(args.ToolCallId)
				if b.callIDs[id] {
					return done(r, errors.New("Cursor tool call ID conflicts with completed history"))
				}
				if !seen[id] {
					r.calls = append(r.calls, toolCall{ID: id, Type: "function", Function: functionCall{Name: name, Arguments: string(rawArgs)}})
					seen[id] = true
				}
				return done(r, nil)
			}
			if exec.GetRequestContextArgs() == nil && exec.GetMcpStateExecArgs() == nil {
				rejectedTools++
				if rejectedTools > maxRejectedToolCalls {
					return done(r, errors.New("Cursor repeatedly requested disabled native tools; no native operation was performed"))
				}
			}
			reply, err := execReply(exec, b.tools)
			if err != nil {
				return done(r, err)
			}
			if err := t.send(reply); err != nil {
				return done(r, err)
			}
		case *pb.AgentServerMessage_InteractionQuery:
			reply, err := interactionReply(m.InteractionQuery, b.tools)
			if err != nil {
				return done(r, err)
			}
			rejectedTools++
			if rejectedTools > maxRejectedToolCalls {
				return done(r, errors.New("Cursor repeatedly requested unsupported interactions; no approval or answer was provided"))
			}
			if err := t.send(reply); err != nil {
				return done(r, err)
			}
		case *pb.AgentServerMessage_ExecServerControlMessage:
		default:
			return done(r, errors.New("unsupported Cursor server message; protocol schema may have changed"))
		}
	}
}
func execReply(exec *pb.ExecServerMessage, tools []*pb.McpToolDefinition) (*pb.AgentClientMessage, error) {
	reply := &pb.ExecClientMessage{Id: exec.Id, ExecId: exec.ExecId}
	switch exec.Message.(type) {
	case *pb.ExecServerMessage_RequestContextArgs:
		reply.Message = &pb.ExecClientMessage_RequestContextResult{RequestContextResult: &pb.RequestContextResult{Result: &pb.RequestContextResult_Success{Success: &pb.RequestContextSuccess{RequestContext: clientRequestContext(tools)}}}}
	case *pb.ExecServerMessage_McpStateExecArgs:
		advertised := make([]*pb.McpToolDefinition, 0, len(tools))
		for _, tool := range tools {
			copy := proto.Clone(tool).(*pb.McpToolDefinition)
			copy.ToolName = "mcp_" + toolProvider + "_" + tool.Name
			advertised = append(advertised, copy)
		}
		servers := []*pb.McpStateServer{}
		if len(tools) > 0 {
			servers = append(servers, &pb.McpStateServer{ServerName: toolProvider, ServerIdentifier: toolProvider, Tools: advertised, Instructions: []*pb.McpInstructions{{ServerName: toolProvider, Instructions: clientToolPolicy(tools)}}})
		}
		reply.Message = &pb.ExecClientMessage_McpStateExecResult{McpStateExecResult: &pb.McpStateExecResult{Result: &pb.McpStateExecResult_Success{Success: &pb.McpStateSuccess{Servers: servers}}}}
	default:
		return refusedNativeExec(exec, tools), nil
	}
	return &pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientMessage{ExecClientMessage: reply}}, nil
}
