//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestQuotaABIPreservesUpstreamStatus(t *testing.T) {
	abiLifecycleMu.Lock()
	drainDispatch()
	plugin.Shutdown()
	plugin = NewPlugin()
	resumeDispatch()
	abiLifecycleMu.Unlock()
	old := callHost
	t.Cleanup(func() { _, _ = handleMethod(pluginabi.MethodPluginShutdown, nil); callHost = old })
	for _, status := range []int{401, 403, 429, 503} {
		callHost = func(method string, _ any) (json.RawMessage, error) {
			if method == pluginabi.MethodHostHTTPOperationOpen {
				return json.RawMessage(`{"operation_id":"quota-op"}`), nil
			}
			return json.Marshal(map[string]any{"StatusCode": status, "Body": []byte("token-secret should never escape")})
		}
		raw, err := handleMethod(pluginabi.MethodQuotaFetch, []byte(`{"provider":"cursor","storage_json":"eyJ0eXBlIjoiY3Vyc29yIiwiaWQiOiJmaXh0dXJlIiwiYWNjZXNzX3Rva2VuIjoiZml4dHVyZS1hY2Nlc3MifQ=="}`))
		if err != nil {
			t.Fatal(err)
		}
		var env pluginabi.Envelope
		if json.Unmarshal(raw, &env) != nil || env.OK || env.Error == nil || env.Error.HTTPStatus != status {
			t.Fatalf("status %d: %s", status, raw)
		}
		if strings.Contains(string(raw), "token-secret") || strings.Contains(string(raw), "fixture-access") {
			t.Fatal("upstream body leaked")
		}
		if env.Error.Retryable != (status == 429 || status == 503) {
			t.Fatalf("status %d retryable=%v", status, env.Error.Retryable)
		}
	}
}

func TestQuotaShutdownCancelsAndDrainsHostRequest(t *testing.T) {
	abiLifecycleMu.Lock()
	drainDispatch()
	plugin.Shutdown()
	plugin = NewPlugin()
	resumeDispatch()
	abiLifecycleMu.Unlock()
	old := callHost
	t.Cleanup(func() { callHost = old })
	entered, cancelled := make(chan struct{}), make(chan struct{})
	var once sync.Once
	callHost = func(method string, _ any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostHTTPOperationOpen:
			return json.RawMessage(`{"operation_id":"quota-op"}`), nil
		case pluginabi.MethodHostHTTPDo:
			close(entered)
			<-cancelled
			return json.RawMessage(`{"StatusCode":200,"Body":"e30="}`), nil
		case pluginabi.MethodHostHTTPCancel:
			once.Do(func() { close(cancelled) })
			return json.RawMessage(`{}`), nil
		default:
			t.Errorf("unexpected callback %s", method)
			return json.RawMessage(`{}`), nil
		}
	}
	fetched := make(chan struct{})
	go func() {
		defer close(fetched)
		_, _ = handleMethod(pluginabi.MethodQuotaFetch, []byte(`{"provider":"cursor","storage_json":"eyJ0eXBlIjoiY3Vyc29yIiwiaWQiOiJmaXh0dXJlIiwiYWNjZXNzX3Rva2VuIjoiZml4dHVyZS1hY2Nlc3MifQ=="}`))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("quota fetch never entered host request")
	}
	stopped := make(chan struct{})
	go func() { _, _ = handleMethod(pluginabi.MethodPluginShutdown, nil); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to cancel and drain quota")
	}
	<-fetched
}
