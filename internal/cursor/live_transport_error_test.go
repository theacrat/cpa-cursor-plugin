package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestLiveTransportChunkStatusesRemainTerminalAndRedacted(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 503} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/wrapped=%v", status, wrapped), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					var failure error = &HTTPStatusError{StatusCode: status}
					if wrapped {
						failure = fmt.Errorf("private-stream-payload: %w", failure)
					}
					err, scope := executeLiveChunkFailure(t, failure)
					var upstream *HTTPStatusError
					if !errors.As(err, &upstream) || upstream.StatusCode != status {
						t.Fatalf("status %d replaced by %v", status, err)
					}
					if err.Error() != (&HTTPStatusError{StatusCode: status}).Error() || strings.Contains(err.Error(), "private-stream-payload") {
						t.Fatalf("status error was not sanitised: %v", err)
					}
					scope.mu.Lock()
					defer scope.mu.Unlock()
					if scope.runs != 1 || scope.results != 0 || scope.attaches != 0 || !scope.closed {
						t.Fatalf("terminal status recovered or leaked: runs=%d results=%d attaches=%d closed=%v", scope.runs, scope.results, scope.attaches, scope.closed)
					}
				})
			})
		}
	}
}

func TestLiveTransportUnknownChunkErrorIsTerminalAndRedacted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		err, scope := executeLiveChunkFailure(t, errors.New("private-stream-payload"))
		if err == nil || err.Error() != "Cursor host stream failed" {
			t.Fatalf("unknown stream error was not fixed and terminal: %v", err)
		}
		scope.mu.Lock()
		defer scope.mu.Unlock()
		if scope.runs != 1 || scope.results != 0 || scope.attaches != 0 || !scope.closed {
			t.Fatalf("unknown stream error recovered or leaked: runs=%d results=%d attaches=%d closed=%v", scope.runs, scope.results, scope.attaches, scope.closed)
		}
	})
}

func executeLiveChunkFailure(t *testing.T, failure error) (error, *liveFixtureScope) {
	t.Helper()
	scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8), afterFirst: true}
	e := NewExecutor(DefaultConfig())
	warmFixtureVariants(e)
	t.Cleanup(e.Close)
	req := liveRequest(&liveFixtureHost{scope: scope})
	first, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Choices []struct{ Message chatMessage }
	}
	if json.Unmarshal(first.Payload, &body) != nil || len(body.Choices) != 1 || len(body.Choices[0].Message.ToolCalls) != 1 {
		t.Fatal("initial request did not park exactly one client tool call")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(req.Payload, &fields) != nil {
		t.Fatal("invalid fixture request")
	}
	var messages []chatMessage
	if json.Unmarshal(fields["messages"], &messages) != nil {
		t.Fatal("invalid fixture history")
	}
	message := body.Choices[0].Message
	messages = append(messages, message, chatMessage{Role: "tool", ToolCallID: message.ToolCalls[0].ID, Content: json.RawMessage(`"actual result"`)})
	fields["messages"], _ = json.Marshal(messages)
	req.Payload, _ = json.Marshal(fields)
	scope.chunks <- pluginapi.HTTPStreamChunk{Err: failure}
	synctest.Wait()
	response, err := e.Execute(context.Background(), req)
	if strings.Contains(string(response.Payload), "private-stream-payload") {
		t.Fatal("stream payload leaked into response")
	}
	return err, scope
}

func TestHostTransportChunkErrorsKeepOnlyKnownCanonicalCauses(t *testing.T) {
	for _, cause := range []error{io.EOF, io.ErrUnexpectedEOF, context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8)}
			e := NewExecutor(DefaultConfig())
			defer e.Close()
			req := testRequest()
			req.HTTPClient = scope
			built, err := buildRequest(req)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := e.hostRun(context.Background(), req, built)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.close()
			scope.chunks <- pluginapi.HTTPStreamChunk{Err: fmt.Errorf("private-stream-payload: %w", cause)}
			reader := transport.reader.(frameReader)
			for {
				_, _, err = readFrame(reader.Reader, reader.encoding)
				if err != nil {
					break
				}
			}
			if err != cause || strings.Contains(err.Error(), "private-stream-payload") {
				t.Fatalf("canonical cause %v changed to %v", cause, err)
			}
		})
	}
}
