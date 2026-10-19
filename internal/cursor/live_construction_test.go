package cursor

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
)

type liveConstructionHost struct {
	*liveFixtureHost
	scope             *liveConstructionScope
	blockOpen         bool
	openEntered       chan struct{}
	openExited        chan struct{}
	openCancelled     atomic.Bool
	opens             atomic.Int32
	release           chan struct{}
	releaseOnce       sync.Once
	gateCapability    bool
	capabilityEntered chan struct{}
	capabilityRelease chan struct{}
}

type liveConstructionScope struct {
	*liveFixtureScope
	blockStream     bool
	streamEntered   chan struct{}
	streamExited    chan struct{}
	streamCancelled atomic.Bool
	workerDone      chan struct{}
	workers         atomic.Int32
	closed          chan struct{}
	closeOnce       sync.Once
	release         <-chan struct{}
}

func newLiveConstructionHost(blockOpen, blockStream bool) *liveConstructionHost {
	release := make(chan struct{})
	return &liveConstructionHost{
		liveFixtureHost: &liveFixtureHost{}, blockOpen: blockOpen,
		openEntered: make(chan struct{}), openExited: make(chan struct{}), release: release,
		capabilityEntered: make(chan struct{}), capabilityRelease: make(chan struct{}),
		scope: &liveConstructionScope{
			liveFixtureScope: &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)},
			blockStream:      blockStream, streamEntered: make(chan struct{}), streamExited: make(chan struct{}),
			workerDone: make(chan struct{}), closed: make(chan struct{}), release: release,
		},
	}
}

func (h *liveConstructionHost) unblock() { h.releaseOnce.Do(func() { close(h.release) }) }

func (h *liveConstructionHost) SupportsRetainedHTTP() bool {
	if h.gateCapability {
		close(h.capabilityEntered)
		<-h.capabilityRelease
	}
	return true
}

func (h *liveConstructionHost) OpenScope(ctx context.Context) (RetainedHTTPScope, error) {
	h.opens.Add(1)
	close(h.openEntered)
	defer close(h.openExited)
	if h.blockOpen {
		select {
		case <-ctx.Done():
			h.openCancelled.Store(true)
			return nil, ctx.Err()
		case <-h.release:
			return nil, errors.New("fixture scope opening released")
		}
	}
	return h.scope, nil
}

func (s *liveConstructionScope) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *liveConstructionScope) DoStream(ctx context.Context, _ pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	close(s.streamEntered)
	defer close(s.streamExited)
	if s.blockStream {
		select {
		case <-ctx.Done():
			s.streamCancelled.Store(true)
			return pluginapi.HTTPStreamResponse{}, ctx.Err()
		case <-s.release:
			return pluginapi.HTTPStreamResponse{}, errors.New("fixture stream opening released")
		}
	}
	s.workers.Add(1)
	go func() {
		defer close(s.workerDone)
		defer s.workers.Add(-1)
		select {
		case <-ctx.Done():
			s.streamCancelled.Store(true)
		case <-s.release:
		}
		close(s.chunks)
	}()
	return pluginapi.HTTPStreamResponse{StatusCode: 200, Headers: http.Header{}, Chunks: s.chunks}, nil
}

func (s *liveConstructionScope) Do(ctx context.Context, _ pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	if err := ctx.Err(); err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	frame, err := frameMessage(update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TextDelta{TextDelta: &pb.TextDeltaUpdate{Text: "active stream"}}}))
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	select {
	case s.chunks <- pluginapi.HTTPStreamChunk{Payload: frame}:
		return pluginapi.HTTPResponse{StatusCode: 200}, nil
	case <-ctx.Done():
		return pluginapi.HTTPResponse{}, ctx.Err()
	}
}

func assertLiveConstructionSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	default:
		t.Errorf("%s did not finish", label)
	}
}

func assertLiveConstructionDrained(t *testing.T, e *Executor, host *liveConstructionHost) {
	t.Helper()
	e.liveMu.Lock()
	remaining := len(e.live)
	e.liveMu.Unlock()
	e.mu.Lock()
	active := len(e.active)
	e.mu.Unlock()
	if remaining != 0 || active != 0 || len(e.slots) != 0 || host.scope.workers.Load() != 0 {
		t.Errorf("construction leaked resources: sessions=%d active=%d slots=%d workers=%d", remaining, active, len(e.slots), host.scope.workers.Load())
	}
}

func TestLiveConstructionCancellationAndCloseDrainBlockedStages(t *testing.T) {
	for _, stage := range []string{"scope", "stream"} {
		for _, stop := range []string{"request", "executor"} {
			t.Run(stage+"/"+stop, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					host := newLiveConstructionHost(stage == "scope", stage == "stream")
					e := NewExecutor(DefaultConfig())
					warmFixtureVariants(e)
					ctx, cancel := context.WithCancel(context.Background())
					defer func() { cancel(); host.unblock(); e.Close() }()
					finished := make(chan error, 1)
					go func() { _, err := e.Execute(ctx, liveRequest(host)); finished <- err }()
					synctest.Wait()
					assertLiveConstructionSignal(t, host.openEntered, "scope entry")
					if stage == "stream" {
						assertLiveConstructionSignal(t, host.scope.streamEntered, "stream entry")
					}
					closed := make(chan struct{})
					if stop == "executor" {
						go func() { e.Close(); close(closed) }()
					} else {
						cancel()
						close(closed)
					}
					synctest.Wait()
					assertLiveConstructionSignal(t, closed, "executor close")
					select {
					case err := <-finished:
						if err == nil {
							t.Error("cancelled construction completed successfully")
						}
					default:
						t.Error("cancelled construction did not return")
					}
					assertLiveConstructionSignal(t, host.openExited, "scope callback")
					if stage == "scope" && !host.openCancelled.Load() {
						t.Error("scope callback did not observe cancellation")
					}
					if stage == "stream" {
						assertLiveConstructionSignal(t, host.scope.streamExited, "stream callback")
						assertLiveConstructionSignal(t, host.scope.closed, "scope cleanup")
						if !host.scope.streamCancelled.Load() {
							t.Error("stream callback did not observe cancellation")
						}
					}
					assertLiveConstructionDrained(t, e, host)
				})
			})
		}
	}
}

func TestLiveConstructionActiveStreamingCancellationDrainsReaderWorkers(t *testing.T) {
	for _, stop := range []string{"request", "executor"} {
		t.Run(stop, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				host := newLiveConstructionHost(false, false)
				e := NewExecutor(DefaultConfig())
				warmFixtureVariants(e)
				ctx, cancel := context.WithCancel(context.Background())
				defer func() { cancel(); host.unblock(); e.Close() }()
				response, err := e.ExecuteStream(ctx, liveRequest(host))
				if err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				e.liveMu.Lock()
				session := e.live[liveOwner(liveRequest(host))]
				e.liveMu.Unlock()
				if session == nil {
					t.Fatal("active stream has no retained session")
				}
				closed := make(chan struct{})
				if stop == "executor" {
					go func() { e.Close(); close(closed) }()
				} else {
					cancel()
					close(closed)
				}
				synctest.Wait()
				assertLiveConstructionSignal(t, closed, "executor close")
				assertLiveConstructionSignal(t, host.scope.closed, "retained scope")
				assertLiveConstructionSignal(t, host.scope.workerDone, "host stream worker")
				assertLiveConstructionSignal(t, session.done, "protocol reader")
				for range response.Chunks {
				}
				if !host.scope.streamCancelled.Load() {
					t.Error("host stream did not observe cancellation")
				}
				assertLiveConstructionDrained(t, e, host)
			})
		})
	}
}

func TestLiveConstructionCancelledBeforeAdmissionDoesNotOpenScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newLiveConstructionHost(true, true)
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer func() { host.unblock(); e.Close() }()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.Execute(ctx, liveRequest(host)); err == nil {
			t.Error("cancelled request was admitted")
		}
		synctest.Wait()
		if host.opens.Load() != 0 {
			t.Error("cancelled request opened a retained scope")
		}
		assertLiveConstructionDrained(t, e, host)
	})
}

func TestLiveConstructionCloseBeforeSessionInsertionDoesNotOpenScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newLiveConstructionHost(true, false)
		host.gateCapability = true
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		defer func() { host.unblock(); e.Close() }()
		finished := make(chan error, 1)
		go func() { _, err := e.Execute(context.Background(), liveRequest(host)); finished <- err }()
		synctest.Wait()
		assertLiveConstructionSignal(t, host.capabilityEntered, "pre-insertion capability check")
		closed := make(chan struct{})
		go func() { e.Close(); close(closed) }()
		synctest.Wait()
		e.mu.Lock()
		stopped := e.closed
		e.mu.Unlock()
		if !stopped {
			t.Error("executor Close did not stop admission")
		}
		close(host.capabilityRelease)
		synctest.Wait()
		assertLiveConstructionSignal(t, closed, "executor close")
		select {
		case err := <-finished:
			if err == nil {
				t.Error("construction succeeded after executor close")
			}
		default:
			t.Error("construction survived executor close")
		}
		if host.opens.Load() != 0 {
			t.Error("construction opened a retained scope after cancellation before insertion")
		}
		assertLiveConstructionDrained(t, e, host)
	})
}

func TestLiveConstructionRequestCancelledBeforeSessionInsertionDoesNotOpenScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		host := newLiveConstructionHost(true, false)
		host.gateCapability = true
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		ctx, cancel := context.WithCancel(context.Background())
		defer func() { cancel(); host.unblock(); e.Close() }()
		finished := make(chan error, 1)
		go func() { _, err := e.Execute(ctx, liveRequest(host)); finished <- err }()
		synctest.Wait()
		assertLiveConstructionSignal(t, host.capabilityEntered, "pre-insertion capability check")
		cancel()
		close(host.capabilityRelease)
		synctest.Wait()
		select {
		case err := <-finished:
			if err == nil {
				t.Error("construction succeeded after request cancellation")
			}
		default:
			t.Error("construction survived request cancellation")
		}
		if host.opens.Load() != 0 {
			t.Error("cancelled request opened a retained scope before insertion")
		}
		assertLiveConstructionDrained(t, e, host)
	})
}
