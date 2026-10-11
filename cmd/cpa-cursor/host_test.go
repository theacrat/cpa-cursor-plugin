package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestHostHTTPContextAndOperation(t *testing.T) {
	old := callHost
	t.Cleanup(func() { callHost = old })
	var methods []string
	callHost = func(method string, value any) (json.RawMessage, error) {
		methods = append(methods, method)
		switch method {
		case pluginabi.MethodHostHTTPOperationOpen:
			if value.(map[string]string)["host_callback_id"] != "callback" {
				t.Fatal("lost callback scope")
			}
			return json.RawMessage(`{"operation_id":"operation"}`), nil
		case pluginabi.MethodHostHTTPDo:
			req := value.(hostRequest)
			if req.CallbackID != "callback" || req.OperationID != "operation" {
				t.Fatal("lost HTTP scope")
			}
			return json.RawMessage(`{"StatusCode":200,"Body":"e30="}`), nil
		default:
			return nil, errors.New("unexpected callback")
		}
	}
	response, err := (hostHTTP{callbackID: "callback"}).Do(context.Background(), pluginapi.HTTPRequest{Method: "GET", URL: "https://example.com"})
	if err != nil || response.StatusCode != 200 || string(response.Body) != "{}" {
		t.Fatalf("response=%v err=%v", response, err)
	}
	if len(methods) != 2 {
		t.Fatal(methods)
	}
}

func TestHostHTTPCancelsBlockedCall(t *testing.T) {
	old := callHost
	t.Cleanup(func() { callHost = old })
	blocked := make(chan struct{})
	cancelled := make(chan struct{})
	var once sync.Once
	callHost = func(method string, _ any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostHTTPOperationOpen:
			return json.RawMessage(`{"operation_id":"operation"}`), nil
		case pluginabi.MethodHostHTTPDo:
			close(blocked)
			<-cancelled
			return nil, errors.New("cancelled")
		case pluginabi.MethodHostHTTPCancel:
			once.Do(func() { close(cancelled) })
			return json.RawMessage(`{}`), nil
		default:
			return nil, errors.New("unexpected callback")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := (hostHTTP{}).Do(ctx, pluginapi.HTTPRequest{}); result <- err }()
	<-blocked
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked callback not cancelled")
	}
}

func TestHostStreamClosesAfterEOF(t *testing.T) {
	old := callHost
	t.Cleanup(func() { callHost = old })
	closed := make(chan struct{})
	callHost = func(method string, _ any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostHTTPOperationOpen:
			return json.RawMessage(`{"operation_id":"operation"}`), nil
		case pluginabi.MethodHostHTTPDoStream:
			return json.RawMessage(`{"status_code":200,"stream_id":"stream"}`), nil
		case pluginabi.MethodHostHTTPStreamRead:
			return json.RawMessage(`{"payload":"aGVsbG8=","done":true}`), nil
		case pluginabi.MethodHostHTTPStreamClose:
			close(closed)
			return json.RawMessage(`{}`), nil
		default:
			return nil, errors.New("unexpected callback")
		}
	}
	stream, err := (hostHTTP{}).DoStream(context.Background(), pluginapi.HTTPRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		body = append(body, chunk.Payload...)
	}
	if string(body) != "hello" {
		t.Fatal(string(body))
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("stream was not closed")
	}
}
