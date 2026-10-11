package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Executor owns active runs, not conversation state. Every request republishes
// the caller's full transcript, including tool results from previous turns.
type Executor struct {
	cfg    Config
	mu     sync.Mutex
	closed bool
	active map[uint64]context.CancelFunc
	next   uint64
	wg     sync.WaitGroup
	slots  chan struct{}
	// client is only injected by package tests. Production transports enforce TLS.
	client *http.Client
}

func (e *Executor) Identifier() string { return "cursor" }

func NewExecutor(cfg Config) *Executor {
	n := cfg.MaxConcurrentStreams
	if n <= 0 {
		n = 8
	}
	return &Executor{cfg: cfg, active: make(map[uint64]context.CancelFunc), slots: make(chan struct{}, n)}
}

func (e *Executor) lease(ctx context.Context) (context.Context, func(), error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, nil, errors.New("cursor executor is closed")
	}
	if !e.cfg.Enabled {
		e.mu.Unlock()
		return nil, nil, errors.New("Cursor provider is disabled")
	}
	timeout := e.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 300
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	e.next++
	id := e.next
	e.active[id] = cancel
	e.wg.Add(1)
	e.mu.Unlock()
	release := func() { cancel(); e.mu.Lock(); delete(e.active, id); e.mu.Unlock(); e.wg.Done() }
	select {
	case e.slots <- struct{}{}:
		return ctx, func() { <-e.slots; release() }, nil
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	}
}

func (e *Executor) Close() {
	e.mu.Lock()
	e.closed = true
	for _, cancel := range e.active {
		cancel()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

func executorToken(storage []byte, metadata map[string]any) (string, error) {
	var data map[string]any
	if len(storage) > 0 && json.Unmarshal(storage, &data) != nil {
		return "", errors.New("invalid Cursor credential storage")
	}
	token, _ := data["access_token"].(string)
	if token == "" {
		token, _ = metadata["access_token"].(string)
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("Cursor access_token is required")
	}
	return token, nil
}

func (e *Executor) CountTokens(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	return pluginapi.ExecutorResponse{}, errors.New("Cursor token counting is unsupported: no exact tokenizer or upstream count endpoint is available")
}
func (e *Executor) HttpRequest(context.Context, pluginapi.ExecutorHTTPRequest) (pluginapi.ExecutorHTTPResponse, error) {
	return pluginapi.ExecutorHTTPResponse{}, errors.New("Cursor arbitrary HTTP forwarding is unsupported")
}

func (e *Executor) Execute(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	ctx, release, err := e.lease(ctx)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	defer release()
	built, err := buildRequest(req)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	result, err := e.run(ctx, req, built, nil)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	msg := map[string]any{"role": "assistant", "content": result.text}
	if result.thinking != "" {
		msg["reasoning_content"] = result.thinking
	}
	reason := "stop"
	if len(result.calls) > 0 {
		msg["tool_calls"] = result.calls
		reason = "tool_calls"
	}
	payload, _ := json.Marshal(map[string]any{"id": built.id, "object": "chat.completion", "created": time.Now().Unix(), "model": built.model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": reason}}, "usage": result.usage()})
	return pluginapi.ExecutorResponse{Payload: payload, Headers: http.Header{"Content-Type": []string{"application/json"}}}, nil
}

func (e *Executor) ExecuteStream(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
	ctx, release, err := e.lease(ctx)
	if err != nil {
		return pluginapi.ExecutorStreamResponse{}, err
	}
	built, err := buildRequest(req)
	if err != nil {
		release()
		return pluginapi.ExecutorStreamResponse{}, err
	}
	if _, err = executorToken(req.StorageJSON, req.AuthMetadata); err != nil {
		release()
		return pluginapi.ExecutorStreamResponse{}, err
	}
	chunks := make(chan pluginapi.ExecutorStreamChunk, 8)
	go func() {
		defer release()
		defer close(chunks)
		send := func(c pluginapi.ExecutorStreamChunk) error {
			select {
			case chunks <- c:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		emit := func(delta map[string]any, finish any, usage any) error {
			body := map[string]any{"id": built.id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": built.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
			if usage != nil {
				body["usage"] = usage
			}
			raw, _ := json.Marshal(body)
			return send(pluginapi.ExecutorStreamChunk{Payload: append(append([]byte("data: "), raw...), []byte("\n\n")...)})
		}
		if err := emit(map[string]any{"role": "assistant"}, nil, nil); err != nil {
			return
		}
		result, err := e.run(ctx, req, built, func(text string, thinking bool) error {
			key := "content"
			if thinking {
				key = "reasoning_content"
			}
			return emit(map[string]any{key: text}, nil, nil)
		})
		if err != nil {
			_ = send(pluginapi.ExecutorStreamChunk{Err: err})
			return
		}
		reason := "stop"
		if len(result.calls) > 0 {
			reason = "tool_calls"
			for i, c := range result.calls {
				if err := emit(map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": c.ID, "type": "function", "function": c.Function}}}, nil, nil); err != nil {
					return
				}
			}
		}
		if err := emit(map[string]any{}, reason, result.usage()); err != nil {
			return
		}
		_ = send(pluginapi.ExecutorStreamChunk{Payload: []byte("data: [DONE]\n\n")})
	}()
	return pluginapi.ExecutorStreamResponse{Headers: http.Header{"Content-Type": []string{"text/event-stream"}}, Chunks: chunks}, nil
}
