package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/theacrat/cpa-cursor-plugin/internal/cursor"
)

func TestRetainedHTTPHostUsesScopedOperations(t *testing.T) {
	old := callHost
	t.Cleanup(func() { callHost = old })
	var methods []string
	callHost = func(method string, request any) (json.RawMessage, error) {
		methods = append(methods, method)
		raw, _ := json.Marshal(request)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		switch method {
		case "host.http.scope_open", "host.http.scope_attach":
			if string(fields["host_callback_id"]) != `"current-request"` {
				t.Fatal("retained scope did not bind the current callback")
			}
			return json.RawMessage(`{"scope_id":"owned-scope","attachment_id":"current-attachment","idle_timeout_ms":3600000}`), nil
		case "host.http.operation_open", "host.http.do":
			if string(fields["scope_id"]) != `"owned-scope"` || len(fields["host_callback_id"]) > 0 {
				t.Fatal("retained operation lost scope ownership")
			}
			if method == "host.http.operation_open" {
				return json.RawMessage(`{"operation_id":"scoped-operation"}`), nil
			}
			return json.RawMessage(`{"StatusCode":200}`), nil
		case "host.http.scope_prepare_park", "host.http.scope_close":
			if string(fields["scope_id"]) != `"owned-scope"` || string(fields["attachment_id"]) != `"current-attachment"` {
				t.Fatal("scope control lost attachment identity")
			}
			return json.RawMessage(`{}`), nil
		default:
			t.Fatalf("unexpected callback %s", method)
			return nil, nil
		}
	}
	var host cursor.RetainedHTTPHost = hostHTTP{callbackID: "current-request", retained: true}
	if !host.SupportsRetainedHTTP() {
		t.Fatal("negotiated capability missing")
	}
	scope, err := host.OpenScope(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Do(context.Background(), pluginapi.HTTPRequest{URL: "https://example.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := scope.PreparePark(context.Background()); err != nil {
		t.Fatal(err)
	}
	attached, err := host.AttachScope(context.Background(), scope.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := attached.Close(); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 6 {
		t.Fatalf("callback count=%d", len(methods))
	}
}

func TestRetainedHTTPHostCapabilityAbsenceFailsWithoutCallbacks(t *testing.T) {
	old := callHost
	t.Cleanup(func() { callHost = old })
	callHost = func(string, any) (json.RawMessage, error) { t.Fatal("unsupported host was called"); return nil, nil }
	host := hostHTTP{callbackID: "request"}
	if host.SupportsRetainedHTTP() {
		t.Fatal("old host gained retained support")
	}
	if _, err := host.OpenScope(context.Background()); err == nil {
		t.Fatal("unsupported scope opened")
	}
}
