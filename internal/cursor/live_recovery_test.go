package cursor

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type liveRecoveryHost struct {
	mu       sync.Mutex
	texts    []string
	complete int
	opens    int
	started  []time.Time
}

type liveRecoveryScope struct {
	*liveFixtureScope
	text     string
	complete bool
}

func (*liveRecoveryHost) SupportsRetainedHTTP() bool { return true }
func (h *liveRecoveryHost) OpenScope(ctx context.Context) (RetainedHTTPScope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.opens++
	h.started = append(h.started, time.Now())
	text := ""
	if h.opens <= len(h.texts) {
		text = h.texts[h.opens-1]
	}
	return &liveRecoveryScope{liveFixtureScope: &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}, text: text, complete: h.opens == h.complete}, nil
}
func (*liveRecoveryHost) AttachScope(context.Context, string) (RetainedHTTPScope, error) {
	return nil, errors.New("unexpected attach")
}
func (*liveRecoveryHost) Do(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	return pluginapi.HTTPResponse{}, errors.New("unexpected request")
}
func (*liveRecoveryHost) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return pluginapi.HTTPStreamResponse{}, errors.New("unexpected stream")
}

func (s *liveRecoveryScope) Do(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	_, _, size := protowire.ConsumeTag(req.Body)
	raw, _ := protowire.ConsumeString(req.Body[size:])
	decoded, _ := hex.DecodeString(raw)
	var message pb.AgentClientMessage
	if proto.Unmarshal(decoded, &message) != nil || message.GetRunRequest() == nil {
		return pluginapi.HTTPResponse{}, errors.New("unexpected recovery append")
	}
	if s.text != "" {
		frame, _ := frameMessage(update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TextDelta{TextDelta: &pb.TextDeltaUpdate{Text: s.text}}}))
		s.chunks <- pluginapi.HTTPStreamChunk{Payload: frame}
	}
	if s.complete {
		frame, _ := frameMessage(endTurn())
		s.chunks <- pluginapi.HTTPStreamChunk{Payload: frame}
	}
	return pluginapi.HTTPResponse{StatusCode: 200}, nil
}

func TestLiveToolLoopStallRecoveryIsBoundedAndBackedOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := &liveRecoveryHost{}
		cfg := DefaultConfig()
		cfg.ExecutionTimeout = 3600
		e := NewExecutor(cfg)
		warmFixtureVariants(e)
		defer e.Close()
		_, err := e.Execute(context.Background(), liveRequest(host))
		if err == nil || !strings.Contains(err.Error(), "stalled") {
			t.Fatalf("unbounded stall result: %v", err)
		}
		host.mu.Lock()
		defer host.mu.Unlock()
		if host.opens != 4 {
			t.Fatalf("attempts=%d", host.opens)
		}
		for index, want := range []time.Duration{181 * time.Second, 182 * time.Second, 184 * time.Second} {
			if got := host.started[index+1].Sub(host.started[index]); got != want {
				t.Fatalf("attempt %d delay=%v want=%v", index, got, want)
			}
		}
	})
}

func TestLiveToolLoopRecoveryDoesNotRepeatVisiblePrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := &liveRecoveryHost{texts: []string{"prefix", "prefix and continuation"}, complete: 2}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		var visible strings.Builder
		req := liveRequest(host)
		b, err := buildRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		result, err := e.runLive(context.Background(), req, b, func(text string, thinking bool) error {
			if !thinking {
				visible.WriteString(text)
			}
			return nil
		}, 0, host)
		if err != nil {
			t.Fatal(err)
		}
		if visible.String() != "prefix and continuation" || result.text != visible.String() {
			t.Fatalf("duplicated recovered output: %q", visible.String())
		}
	})
}

func TestLiveToolLoopRejectsDivergentRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := &liveRecoveryHost{texts: []string{"original", "different"}, complete: 2}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		_, err := e.Execute(context.Background(), liveRequest(host))
		if err == nil || !strings.Contains(err.Error(), "diverged") {
			t.Fatalf("divergent recovery accepted: %v", err)
		}
	})
}

func TestLiveToolLoopVisibleRecoveryCannotRepeatForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := &liveRecoveryHost{texts: []string{"prefix", "prefix", "prefix"}}
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer e.Close()
		_, err := e.Execute(context.Background(), liveRequest(host))
		if err == nil {
			t.Fatal("repeated prefix gained another recovery")
		}
		host.mu.Lock()
		defer host.mu.Unlock()
		if host.opens != 2 {
			t.Fatalf("visible attempts=%d", host.opens)
		}
	})
}
