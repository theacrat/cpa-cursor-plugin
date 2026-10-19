package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

func TestRetainedHTTPHostAcquisitionErrorsCloseKnownHandles(t *testing.T) {
	for _, acquisition := range []string{"open", "attach"} {
		for _, tc := range []struct {
			name, response, message string
			cancelled               bool
		}{
			{"malformed", `{"scope_id":"allocated-scope","attachment_id":"allocated-attachment","scope_id":17}`, "invalid host callback response", false},
			{"cancelled malformed", `{"scope_id":"allocated-scope","attachment_id":"allocated-attachment","scope_id":17}`, "invalid host callback response", true},
			{"cancelled valid", `{"scope_id":"allocated-scope","attachment_id":"allocated-attachment"}`, "", true},
		} {
			t.Run(acquisition+"/"+tc.name, func(t *testing.T) {
				old := callHost
				t.Cleanup(func() { callHost = old })
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				closes := 0
				callHost = func(method string, request any) (json.RawMessage, error) {
					switch method {
					case "host.http.scope_" + acquisition:
						if tc.cancelled {
							cancel()
						}
						return json.RawMessage(tc.response), nil
					case "host.http.scope_close":
						closes++
						raw, _ := json.Marshal(request)
						var fields map[string]string
						if json.Unmarshal(raw, &fields) != nil || fields["scope_id"] != "allocated-scope" || fields["attachment_id"] != "allocated-attachment" {
							t.Fatalf("cleanup lost returned handles: %s", raw)
						}
						return nil, errors.New("cleanup unavailable")
					default:
						t.Fatalf("unexpected callback %s", method)
						return nil, nil
					}
				}
				host := hostHTTP{callbackID: "current-request", retained: true}
				var scope cursor.RetainedHTTPScope
				var err error
				if acquisition == "attach" {
					scope, err = host.AttachScope(ctx, "allocated-scope")
				} else {
					scope, err = host.OpenScope(ctx)
				}
				if scope != nil || err == nil || closes != 1 {
					t.Fatalf("scope=%v error=%v closes=%d", scope, err, closes)
				}
				if tc.message == "" {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation changed to %v", err)
					}
				} else if err.Error() != tc.message {
					t.Fatalf("acquisition error changed to %v", err)
				}
			})
		}
	}
}

func TestRetainedHTTPHostAttachRejectsMismatchedScopeAndClosesReturnedHandles(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			old := callHost
			t.Cleanup(func() { callHost = old })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closes := 0
			callHost = func(method string, request any) (json.RawMessage, error) {
				switch method {
				case "host.http.scope_attach":
					if cancelled {
						cancel()
					}
					return json.RawMessage(`{"scope_id":"different-scope","attachment_id":"returned-attachment"}`), nil
				case "host.http.scope_close":
					closes++
					raw, _ := json.Marshal(request)
					var fields map[string]string
					if json.Unmarshal(raw, &fields) != nil || fields["scope_id"] != "different-scope" || fields["attachment_id"] != "returned-attachment" {
						t.Fatalf("cleanup did not use returned ownership: %s", raw)
					}
					return nil, errors.New("cleanup unavailable")
				default:
					t.Fatalf("unexpected callback %s", method)
					return nil, nil
				}
			}
			scope, err := (hostHTTP{callbackID: "current-request", retained: true}).AttachScope(ctx, "requested-scope")
			if scope != nil || err == nil || closes != 1 {
				t.Fatalf("scope=%v error=%v closes=%d", scope, err, closes)
			}
			if strings.Contains(err.Error(), "different-scope") || strings.Contains(err.Error(), "returned-attachment") {
				t.Fatalf("mismatch error exposed ownership handles: %v", err)
			}
		})
	}
}

func TestRetainedHTTPHostAttachAcceptsOmittedScopeID(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			old := callHost
			t.Cleanup(func() { callHost = old })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closes := 0
			callHost = func(method string, request any) (json.RawMessage, error) {
				switch method {
				case "host.http.scope_attach":
					if cancelled {
						cancel()
					}
					return json.RawMessage(`{"attachment_id":"new-attachment"}`), nil
				case "host.http.scope_close":
					closes++
					raw, _ := json.Marshal(request)
					var fields map[string]string
					if json.Unmarshal(raw, &fields) != nil || fields["scope_id"] != "requested-scope" || fields["attachment_id"] != "new-attachment" {
						t.Fatalf("omitted-ID cleanup lost requested identity: %s", raw)
					}
					return json.RawMessage(`{}`), nil
				default:
					t.Fatalf("unexpected callback %s", method)
					return nil, nil
				}
			}
			scope, err := (hostHTTP{callbackID: "current-request", retained: true}).AttachScope(ctx, "requested-scope")
			if cancelled {
				if scope != nil || !errors.Is(err, context.Canceled) || closes != 1 {
					t.Errorf("cancelled attach scope=%v error=%v closes=%d", scope, err, closes)
				}
			} else {
				if err != nil || scope == nil || scope.ID() != "requested-scope" {
					t.Errorf("omitted scope ID rejected: scope=%v error=%v", scope, err)
				} else if err := scope.Close(); err != nil || closes != 1 {
					t.Errorf("attached scope close failed: error=%v closes=%d", err, closes)
				}
			}
		})
	}
}

func TestRetainedHTTPHostIncompleteHandlesFailClosed(t *testing.T) {
	for _, acquisition := range []string{"open", "attach"} {
		responses := []string{`{}`, `null`, `{"scope_id":"known-scope"}`, `{"attachment_id":17}`, `{"attachment_id":null}`}
		if acquisition == "open" {
			responses = append(responses, `{"attachment_id":"attachment"}`)
		}
		for _, response := range responses {
			t.Run(acquisition+"/"+response, func(t *testing.T) {
				old := callHost
				t.Cleanup(func() { callHost = old })
				calls := 0
				callHost = func(method string, _ any) (json.RawMessage, error) {
					calls++
					if method != "host.http.scope_"+acquisition {
						t.Fatalf("incomplete ownership reached callback %s", method)
					}
					return json.RawMessage(response), nil
				}
				host := hostHTTP{callbackID: "current-request", retained: true}
				var scope cursor.RetainedHTTPScope
				var err error
				if acquisition == "attach" {
					scope, err = host.AttachScope(context.Background(), "requested-scope")
				} else {
					scope, err = host.OpenScope(context.Background())
				}
				if scope != nil || err == nil || calls != 1 {
					t.Fatalf("incomplete ownership accepted: scope=%v error=%v calls=%d", scope, err, calls)
				}
			})
		}
	}
}
