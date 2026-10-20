package cursor

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type liveFrame struct {
	message *pb.AgentServerMessage
	err     error
}
type livePending struct {
	call toolCall
	exec *pb.ExecServerMessage
}

type liveRetry struct {
	delay   time.Duration
	message string
}

func (e *liveRetry) Error() string { return e.message }

func recoverableLiveTransport(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

type liveSession struct {
	mu                sync.Mutex
	owner, compatible string
	built             *builtRequest
	transport         *runTransport
	scope             RetainedHTTPScope
	ctx               context.Context
	cancel            context.CancelFunc
	frames            chan liveFrame
	done              chan struct{}
	pending           []livePending
	expected          []chatMessage
	parked            bool
	last              time.Time
	output, total     int
	rejections        int
	closed            bool
	closeOnce         sync.Once
	expiry            *time.Timer
	dead              atomic.Bool
	terminalMu        sync.Mutex
	terminalErr       error
	retainedBytes     atomic.Int64
	generation        atomic.Uint64
	replayBytes       int
	pendingBytes      int
}

func (s *liveSession) close() {
	s.closeOnce.Do(func() {
		if s.expiry != nil {
			s.expiry.Stop()
		}
		s.cancel()
		if s.transport != nil {
			s.transport.close()
			<-s.done
		}
		if s.scope != nil {
			_ = s.scope.Close()
		}
	})
}

func (e *Executor) liveSession(requestCtx context.Context, req pluginapi.ExecutorRequest, b *builtRequest, host RetainedHTTPHost) (*liveSession, bool, error) {
	if err := requestCtx.Err(); err != nil {
		return nil, false, err
	}
	owner := liveOwner(req)
	compatible := liveCompatibility(req, b)
	e.liveMu.Lock()
	if err := requestCtx.Err(); err != nil {
		e.liveMu.Unlock()
		return nil, false, err
	}
	if e.live == nil {
		e.live = map[string]*liveSession{}
	}
	session := e.live[owner]
	if session != nil {
		if !session.mu.TryLock() {
			e.liveMu.Unlock()
			return nil, false, errors.New("Cursor conversation already has an active request")
		}
		if session.closed || session.compatible != compatible || time.Since(session.last) > time.Hour {
			delete(e.live, owner)
			session.closed = true
			e.liveMu.Unlock()
			session.close()
			session.mu.Unlock()
			return e.liveSession(requestCtx, req, b, host)
		}
	}
	if session != nil {
		e.liveMu.Unlock()
		return session, true, nil
	}
	if len(e.live) >= 24 {
		e.liveMu.Unlock()
		return nil, false, errors.New("Cursor retained conversation capacity is full")
	}
	var retained int64
	for _, candidate := range e.live {
		retained += candidate.retainedBytes.Load()
	}
	if retained+int64(b.blobs.size+len(req.Payload)) > 256<<20 {
		e.liveMu.Unlock()
		return nil, false, errors.New("Cursor retained blob capacity is full")
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopRequest := context.AfterFunc(requestCtx, cancel)
	defer stopRequest()
	session = &liveSession{owner: owner, compatible: compatible, built: b, ctx: ctx, cancel: cancel, frames: make(chan liveFrame, 1), done: make(chan struct{}), last: time.Now(), replayBytes: len(req.Payload)}
	session.mu.Lock()
	session.retainedBytes.Store(int64(b.blobs.size + len(req.Payload)))
	e.live[owner] = session
	e.liveMu.Unlock()
	if err := requestCtx.Err(); err != nil {
		cancel()
		e.retireLive(session)
		session.mu.Unlock()
		return nil, false, err
	}
	scope, err := host.OpenScope(ctx)
	if err != nil {
		cancel()
		session.mu.Unlock()
		e.liveMu.Lock()
		if e.live[owner] == session {
			delete(e.live, owner)
		}
		e.liveMu.Unlock()
		return nil, false, err
	}
	session.scope = scope
	req.HTTPClient = scope
	transport, err := e.hostRun(ctx, req, b)
	if err != nil {
		cancel()
		_ = scope.Close()
		session.mu.Unlock()
		e.liveMu.Lock()
		if e.live[owner] == session {
			delete(e.live, owner)
		}
		e.liveMu.Unlock()
		return nil, false, err
	}
	session.transport = transport
	go session.read()
	return session, false, nil
}

func (s *liveSession) read() {
	defer close(s.done)
	reader := s.transport.reader.(frameReader)
	for {
		flags, raw, err := readFrame(reader.Reader, reader.encoding)
		message := new(pb.AgentServerMessage)
		if err == nil && flags&2 != 0 {
			var trailer struct {
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &trailer) != nil {
				err = errors.New("invalid Cursor Connect trailer")
			} else if trailer.Error != nil {
				err = connectStatus(trailer.Error.Code)
			} else {
				err = errors.New("Cursor stream ended before turn completion")
			}
		}
		if err == nil && proto.Unmarshal(raw, message) != nil {
			err = errors.New("invalid Cursor server protobuf")
		}
		if update := message.GetInteractionUpdate(); err == nil && update != nil && update.GetHeartbeat() != nil {
			continue
		}
		if err != nil {
			s.terminalMu.Lock()
			s.terminalErr = err
			s.terminalMu.Unlock()
			s.dead.Store(true)
		}
		select {
		case s.frames <- liveFrame{message: message, err: err}:
		case <-s.ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (e *Executor) retireLive(s *liveSession) {
	e.liveMu.Lock()
	if e.live[s.owner] == s {
		delete(e.live, s.owner)
	}
	e.liveMu.Unlock()
	s.closed = true
	s.close()
}

func (e *Executor) updateLiveRetained(s *liveSession) error {
	e.liveMu.Lock()
	defer e.liveMu.Unlock()
	next := int64(s.built.blobs.size + s.replayBytes + s.pendingBytes)
	var retained int64
	for _, candidate := range e.live {
		if candidate == s {
			continue
		}
		retained += candidate.retainedBytes.Load()
	}
	if retained+next > 256<<20 {
		return errors.New("Cursor retained blob capacity is full")
	}
	s.retainedBytes.Store(next)
	return nil
}

func liveReplayBudget(s *liveSession, result runResult) error {
	message := chatMessage{Role: "assistant", Content: json.RawMessage(`""`), Reasoning: result.thinking, ToolCalls: result.calls}
	message.Content, _ = json.Marshal(result.text)
	raw, err := json.Marshal(message)
	if err != nil || s.replayBytes+len(raw)+(64<<10) > maxFrame {
		return errors.New("Cursor client tool batch exceeds replay limit")
	}
	return nil
}

func (e *Executor) runLive(ctx context.Context, req pluginapi.ExecutorRequest, b *builtRequest, onText func(string, bool) error, tableTook time.Duration, host RetainedHTTPHost) (runResult, error) {
	progress := newLiveProgress(time.Now(), false)
	e.liveMu.Lock()
	if session := e.live[liveOwner(req)]; session != nil {
		progress.resumed = true
	}
	e.liveMu.Unlock()
	var combined runResult
	for attempt := 0; attempt <= 3; attempt++ {
		progress.restart(time.Now())
		textOffset, thinkingOffset := 0, 0
		emit := func(text string, thinking bool) error {
			progress.observe(time.Now(), text, thinking)
			offset, existing := &textOffset, &combined.text
			if thinking {
				offset, existing = &thinkingOffset, &combined.thinking
			}
			already := len(*existing) - *offset
			if already < 0 {
				already = 0
			}
			match := len(text)
			if match > already {
				match = already
			}
			if match > 0 && (*existing)[*offset:*offset+match] != text[:match] {
				return errors.New("Cursor recovery diverged from previously streamed output")
			}
			*offset += len(text)
			text = text[match:]
			if text == "" {
				return nil
			}
			*existing += text
			if onText != nil {
				return onText(text, thinking)
			}
			return nil
		}
		result, err := e.runLiveAttempt(ctx, req, b, emit, tableTook, host, &progress)
		combined.output += result.output
		combined.total = result.total
		combined.calls = result.calls
		if err == nil {
			if textOffset < len(combined.text) || thinkingOffset < len(combined.thinking) {
				return combined, errors.New("Cursor recovery ended before preserving prior streamed output")
			}
			return combined, nil
		}
		var retry *liveRetry
		if !errors.As(err, &retry) || attempt == 3 {
			return combined, err
		}
		timer := time.NewTimer(retry.delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return combined, ctx.Err()
		}
	}
	return combined, errors.New("Cursor recovery limit exceeded")
}

func (e *Executor) restoreParkedCatalogue(req pluginapi.ExecutorRequest, b *builtRequest) {
	if len(b.tools) > 0 || !toolResultContinuation(req.Payload) {
		return
	}
	owner := liveOwner(req)
	if owner == "" {
		return
	}
	e.liveMu.Lock()
	session := e.live[owner]
	if session == nil || !session.mu.TryLock() {
		e.liveMu.Unlock()
		return
	}
	e.liveMu.Unlock()
	defer session.mu.Unlock()
	if session.closed || !session.parked || session.built == nil || len(session.built.tools) == 0 {
		return
	}
	b.tools = session.built.tools
	b.wire = session.built.wire
	b.resolve = session.built.resolve
}

func toolResultContinuation(payload []byte) bool {
	var chat chatRequest
	if json.Unmarshal(payload, &chat) != nil || len(chat.Messages) == 0 {
		return false
	}
	return chat.Messages[len(chat.Messages)-1].Role == "tool"
}

func (e *Executor) runLiveAttempt(ctx context.Context, req pluginapi.ExecutorRequest, b *builtRequest, onText func(string, bool) error, tableTook time.Duration, host RetainedHTTPHost, progress *liveProgress) (result runResult, err error) {
	session, resume, err := e.liveSession(ctx, req, b, host)
	if err != nil {
		return result, err
	}
	defer session.mu.Unlock()
	keep := false
	defer func() {
		if !keep {
			e.retireLive(session)
		}
	}()
	timing := runTiming{start: time.Now(), table: tableTook, inBytes: len(req.Payload)}
	defer func() {
		timing.outBytes = len(result.text) + len(result.thinking)
		timing.failed = err != nil
		e.logTiming(b.model, timing)
	}()
	stopCancel := context.AfterFunc(ctx, session.cancel)
	defer stopCancel()
	startOutput := session.output
	countUsage := true
	defer func() {
		if countUsage {
			result.output = session.output - startOutput
			result.total = session.total
		}
	}()
	if session.expiry != nil {
		session.generation.Add(1)
		session.expiry.Stop()
		session.expiry = nil
	}
	if resume {
		if !session.parked {
			return result, errors.New("Cursor conversation is not parked")
		}
		var transcript chatRequest
		_ = json.Unmarshal(req.Payload, &transcript)
		if len(transcript.Messages) > 0 && transcript.Messages[len(transcript.Messages)-1].Role == "user" {
			e.retireLive(session)
			stopCancel()
			countUsage = false
			return e.runLiveAttempt(ctx, req, b, onText, tableTook, host, progress)
		}
		results, matchErr := session.results(req.Payload)
		if matchErr != nil {
			return result, matchErr
		}
		if session.dead.Load() {
			session.terminalMu.Lock()
			terminalErr := session.terminalErr
			session.terminalMu.Unlock()
			if terminalErr != nil && !recoverableLiveTransport(terminalErr) {
				return result, terminalErr
			}
			stopCancel()
			e.retireLive(session)
			if delay, ok := progress.claimRecovery(time.Now()); ok {
				return result, &liveRetry{delay: delay, message: "Cursor parked stream closed before result continuation"}
			}
			return result, errors.New("Cursor live recovery limit exceeded")
		}
		attached, attachErr := host.AttachScope(ctx, session.scope.ID())
		if attachErr != nil {
			return result, attachErr
		}
		session.scope = attached
		for _, message := range results {
			if sendErr := session.transport.send(message); sendErr != nil {
				return result, sendErr
			}
		}
		session.pending = nil
		session.pendingBytes = 0
		session.replayBytes = len(req.Payload)
		if err := e.updateLiveRetained(session); err != nil {
			return result, err
		}
		session.parked = false
		session.rejections = 0
		progress.resumed = true
	}
	var debounce *time.Timer
	var batch <-chan time.Time
	watchdog := time.NewTicker(time.Second)
	defer watchdog.Stop()
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-session.ctx.Done():
			return result, errors.New("Cursor retained run was cancelled")
		case now := <-watchdog.C:
			if progress.expired(now) {
				if len(result.calls) == 0 {
					if delay, ok := progress.recover(now); ok {
						return result, &liveRetry{delay: delay, message: "Cursor live stream stalled without forward progress"}
					}
				}
				return result, errors.New("Cursor live stream stalled without forward progress")
			}
		case <-batch:
			if err := liveReplayBudget(session, result); err != nil {
				return result, err
			}
			expected, historyErr := liveExpectedHistory(req.Payload, result)
			if historyErr != nil {
				return result, historyErr
			}
			rawHistory, _ := json.Marshal(expected)
			session.replayBytes = len(rawHistory)
			if err := e.updateLiveRetained(session); err != nil {
				return result, err
			}
			if parkErr := session.scope.PreparePark(ctx); parkErr != nil {
				return result, parkErr
			}
			if !stopCancel() && ctx.Err() != nil {
				return result, ctx.Err()
			}
			session.expected = expected
			session.parked = true
			session.last = time.Now()
			epoch := session.generation.Add(1)
			session.expiry = time.AfterFunc(time.Hour, func() {
				session.mu.Lock()
				defer session.mu.Unlock()
				if session.generation.Load() != epoch || !session.parked {
					return
				}
				session.cancel()
				e.retireLive(session)
			})
			keep = true
			return result, nil
		case frame := <-session.frames:
			if frame.err != nil {
				if resume && len(result.calls) == 0 && result.text == "" && result.thinking == "" {
					if recoverableLiveTransport(frame.err) {
						stopCancel()
						e.retireLive(session)
						if delay, ok := progress.claimRecovery(time.Now()); ok {
							return result, &liveRetry{delay: delay, message: "Cursor retained stream closed during result continuation"}
						}
						return result, errors.New("Cursor live recovery limit exceeded")
					}
				}
				if errors.Is(frame.err, io.EOF) {
					return result, errors.New("Cursor retained stream ended before turn completion")
				}
				return result, frame.err
			}
			before := len(result.calls)
			ended, handleErr := e.handleLiveFrame(session, b, frame.message, &result, onText)
			if handleErr != nil {
				return result, handleErr
			}
			if len(result.calls) > before {
				if debounce == nil {
					debounce = time.NewTimer(250 * time.Millisecond)
				} else {
					if !debounce.Stop() {
						select {
						case <-debounce.C:
						default:
						}
					}
					debounce.Reset(250 * time.Millisecond)
				}
				batch = debounce.C
			}
			if ended {
				if len(result.calls) > 0 {
					return result, errors.New("Cursor completed a run with unresolved client calls")
				}
				return result, nil
			}
		}
	}
}

func (e *Executor) handleLiveExec(s *liveSession, b *builtRequest, exec *pb.ExecServerMessage, result *runResult) error {
	if args := exec.GetMcpArgs(); args != nil {
		name := s.built.resolve[args.ToolName]
		if name == "" {
			name = s.built.resolve[args.Name]
		}
		if name != "" && (args.ProviderIdentifier == "" || args.ProviderIdentifier == toolProvider) {
			id := clientToolCallID(args.ToolCallId)
			if b.callIDs[id] {
				return errors.New("Cursor tool call ID conflicts with completed history")
			}
			for _, pending := range s.pending {
				if pending.call.ID == id {
					return errors.New("Cursor repeated a pending tool call ID")
				}
				if pending.exec.Id == exec.Id && pending.exec.ExecId == exec.ExecId {
					return errors.New("Cursor repeated a pending execution identity")
				}
			}
			decoded := map[string]any{}
			for key, value := range args.Args {
				v := new(structpb.Value)
				if proto.Unmarshal(value, v) == nil && v.Kind != nil {
					decoded[key] = v.AsInterface()
				} else {
					decoded[key] = string(value)
				}
			}
			raw, _ := json.Marshal(decoded)
			call := toolCall{ID: id, Type: "function", Function: functionCall{Name: name, Arguments: string(raw)}}
			if len(s.pending) >= 64 {
				return errors.New("Cursor client tool batch exceeds limit")
			}
			candidate := *result
			candidate.calls = append(append([]toolCall(nil), result.calls...), call)
			if err := liveReplayBudget(s, candidate); err != nil {
				return err
			}
			identity := &pb.ExecServerMessage{Id: exec.Id, ExecId: exec.ExecId}
			s.pendingBytes += len(raw) + len(id) + len(name) + proto.Size(identity)
			if err := e.updateLiveRetained(s); err != nil {
				return err
			}
			s.pending = append(s.pending, livePending{call: call, exec: identity})
			result.calls = append(result.calls, call)
			return nil
		}
		s.rejections++
		e.logToolRejection(b, exec, s.rejections, "refused")
		if s.rejections > maxRejectedToolCalls {
			return errors.New("Cursor repeatedly requested unavailable tools")
		}
		reply := &pb.ExecClientMessage{Id: exec.Id, ExecId: exec.ExecId, Message: &pb.ExecClientMessage_McpResult{McpResult: &pb.McpResult{Result: &pb.McpResult_ToolNotFound{ToolNotFound: &pb.McpToolNotFound{Name: first(args.ToolName, args.Name), AvailableTools: advertisedClientTools(s.built.tools)}}}}}
		return s.transport.send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientMessage{ExecClientMessage: reply}})
	}
	if exec.GetRequestContextArgs() == nil && exec.GetMcpStateExecArgs() == nil {
		s.rejections++
		e.logToolRejection(b, exec, s.rejections, "refused")
		if s.rejections > maxRejectedToolCalls {
			return errors.New("Cursor repeatedly requested disabled native tools")
		}
	}
	reply, err := execReply(exec, s.built.tools)
	if err != nil {
		return err
	}
	return s.transport.send(reply)
}

func liveExpectedHistory(payload []byte, result runResult) ([]chatMessage, error) {
	var chat chatRequest
	if json.Unmarshal(payload, &chat) != nil {
		return nil, errors.New("invalid Cursor live transcript")
	}
	content, _ := json.Marshal(result.text)
	chat.Messages = append(chat.Messages, chatMessage{Role: "assistant", Content: content, Reasoning: result.thinking, ToolCalls: result.calls})
	return chat.Messages, nil
}

func (s *liveSession) results(payload []byte) ([]*pb.AgentClientMessage, error) {
	messages, err := liveResultMessages(s.expected, s.pending, payload)
	if err != nil {
		return nil, err
	}
	replies := make([]*pb.AgentClientMessage, 0, len(s.pending))
	for index, pending := range s.pending {
		message := messages[index]
		text, images, err := content(message.Content)
		if err != nil {
			return nil, err
		}
		items := []*pb.McpToolResultContentItem{{Content: &pb.McpToolResultContentItem_Text{Text: &pb.McpTextContent{Text: text}}}}
		for _, image := range images {
			items = append(items, &pb.McpToolResultContentItem{Content: &pb.McpToolResultContentItem_Image{Image: &pb.McpImageContent{Data: image.GetData(), MimeType: image.MimeType}}})
		}
		result := &pb.McpResult{Result: &pb.McpResult_Success{Success: &pb.McpSuccess{Content: items}}}
		if message.IsError {
			result.Result = &pb.McpResult_Error{Error: &pb.McpError{Error: text}}
		}
		replies = append(replies, &pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientMessage{ExecClientMessage: &pb.ExecClientMessage{Id: pending.exec.Id, ExecId: pending.exec.ExecId, Message: &pb.ExecClientMessage_McpResult{McpResult: result}}}})
	}
	return replies, nil
}

func canonicalLiveMessage(message chatMessage) chatMessage {
	message.ToolCalls = append([]toolCall(nil), message.ToolCalls...)
	for index := range message.ToolCalls {
		var arguments map[string]any
		decoder := json.NewDecoder(strings.NewReader(message.ToolCalls[index].Function.Arguments))
		decoder.UseNumber()
		if json.Valid([]byte(message.ToolCalls[index].Function.Arguments)) && decoder.Decode(&arguments) == nil && arguments != nil {
			canonical, err := json.Marshal(arguments)
			if err == nil {
				message.ToolCalls[index].Function.Arguments = string(canonical)
			}
		}
	}
	text, images, err := content(message.Content)
	if err == nil && len(images) == 0 {
		message.Content, _ = json.Marshal(text)
	}
	return message
}

func (e *Executor) handleLiveFrame(s *liveSession, b *builtRequest, message *pb.AgentServerMessage, result *runResult, onText func(string, bool) error) (bool, error) {
	switch m := message.Message.(type) {
	case *pb.AgentServerMessage_InteractionUpdate:
		switch update := m.InteractionUpdate.Message.(type) {
		case *pb.InteractionUpdate_TextDelta:
			if len(result.text)+len(update.TextDelta.Text) > maxFrame {
				return false, errors.New("Cursor output exceeds limit")
			}
			result.text += update.TextDelta.Text
			if onText != nil {
				return false, onText(update.TextDelta.Text, false)
			}
		case *pb.InteractionUpdate_ThinkingDelta:
			if len(result.thinking)+len(update.ThinkingDelta.Text) > maxFrame {
				return false, errors.New("Cursor thinking exceeds limit")
			}
			result.thinking += update.ThinkingDelta.Text
			if onText != nil {
				return false, onText(update.ThinkingDelta.Text, true)
			}
		case *pb.InteractionUpdate_TokenDelta:
			if update.TokenDelta.Tokens < 0 {
				return false, errors.New("invalid Cursor token delta")
			}
			s.output += int(update.TokenDelta.Tokens)
		case *pb.InteractionUpdate_TurnEnded:
			return true, nil
		}
	case *pb.AgentServerMessage_ConversationCheckpointUpdate:
		if detail := m.ConversationCheckpointUpdate.TokenDetails; detail != nil {
			s.total = int(detail.UsedTokens)
		}
	case *pb.AgentServerMessage_KvServerMessage:
		kv := m.KvServerMessage
		reply := &pb.KvClientMessage{Id: kv.Id}
		switch request := kv.Message.(type) {
		case *pb.KvServerMessage_GetBlobArgs:
			data, ok := s.built.blobs.data[hex.EncodeToString(request.GetBlobArgs.BlobId)]
			if !ok {
				return false, errors.New("Cursor requested a missing conversation blob")
			}
			reply.Message = &pb.KvClientMessage_GetBlobResult{GetBlobResult: &pb.GetBlobResult{BlobData: data}}
		case *pb.KvServerMessage_SetBlobArgs:
			e.liveMu.Lock()
			var total int64
			for _, candidate := range e.live {
				total += candidate.retainedBytes.Load()
			}
			old := len(s.built.blobs.data[hex.EncodeToString(request.SetBlobArgs.BlobId)])
			if total+int64(len(request.SetBlobArgs.BlobData)-old) > 256<<20 {
				e.liveMu.Unlock()
				return false, errors.New("Cursor retained blob capacity is full")
			}
			if err := s.built.blobs.set(request.SetBlobArgs.BlobId, request.SetBlobArgs.BlobData); err != nil {
				e.liveMu.Unlock()
				return false, err
			}
			s.retainedBytes.Store(int64(s.built.blobs.size + s.replayBytes + s.pendingBytes))
			e.liveMu.Unlock()
			reply.Message = &pb.KvClientMessage_SetBlobResult{SetBlobResult: &pb.SetBlobResult{}}
		default:
			return false, errors.New("unsupported Cursor KV request")
		}
		return false, s.transport.send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_KvClientMessage{KvClientMessage: reply}})
	case *pb.AgentServerMessage_ExecServerMessage:
		return false, e.handleLiveExec(s, b, m.ExecServerMessage, result)
	case *pb.AgentServerMessage_InteractionQuery:
		reply, err := interactionReply(m.InteractionQuery, s.built.tools)
		s.rejections++
		e.logToolRejection(b, m.InteractionQuery, s.rejections, "refused")
		if err != nil {
			return false, err
		}
		if s.rejections > maxRejectedToolCalls {
			return false, errors.New("Cursor repeatedly requested unsupported interactions")
		}
		return false, s.transport.send(reply)
	case *pb.AgentServerMessage_ExecServerControlMessage:
	default:
		return false, errors.New("unsupported Cursor server message")
	}
	return false, nil
}
