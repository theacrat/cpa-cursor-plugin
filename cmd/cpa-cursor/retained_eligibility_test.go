package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/theacrat/cpa-cursor-plugin/internal/cursor"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
)

func TestRetainedHTTPDispatchRequiresRequestEligibility(t *testing.T) {
	old := callHost
	t.Cleanup(func() { callHost = old })
	catalogue, _ := proto.Marshal(&pb.GetUsableModelsResponse{Models: []*pb.ModelDetails{{ModelId: "fixture"}}})
	for _, eligible := range []any{nil, false, "true"} {
		p := NewPlugin()
		p.executor.Log = nil
		p.retainedHTTP = true
		opened := 0
		callHost = func(method string, request any) (json.RawMessage, error) {
			switch method {
			case "host.http.scope_open":
				opened++
				return nil, errors.New("ineligible retained acquisition")
			case pluginabi.MethodHostHTTPOperationOpen:
				return json.RawMessage(`{"operation_id":"operation"}`), nil
			case pluginabi.MethodHostHTTPDo:
				return json.Marshal(pluginapi.HTTPResponse{StatusCode: 200, Body: catalogue})
			case pluginabi.MethodHostHTTPDoStream:
				return json.RawMessage(`{"status_code":403,"stream_id":"stream"}`), nil
			case pluginabi.MethodHostHTTPStreamRead:
				return json.RawMessage(`{"done":true}`), nil
			case pluginabi.MethodHostHTTPStreamClose, pluginabi.MethodHostHTTPCancel:
				return json.RawMessage(`{}`), nil
			default:
				return nil, errors.New("unexpected callback")
			}
		}
		req := pluginapi.ExecutorRequest{Model: "fixture", Format: "chat-completions", AuthID: "account", AuthProvider: "cursor", StorageJSON: []byte(`{"type":"cursor","access_token":"fixture-access"}`), Headers: map[string][]string{"X-Opencode-Session-Id": {"actual-session"}, "X-Cpa-Request-Kind": {"primary"}}, Metadata: map[string]any{"caller_scope": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "request_path": "/v1/chat/completions", "retained_http_eligible": eligible}, Payload: []byte(`{"messages":[{"role":"user","content":"task"}]}`)}
		raw, _ := json.Marshal(req)
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal(raw, &envelope)
		envelope["host_callback_id"] = json.RawMessage(`"request"`)
		raw, _ = json.Marshal(envelope)
		_, err := p.dispatch(pluginabi.MethodExecutorExecute, raw)
		var upstream *cursor.HTTPStatusError
		if !errors.As(err, &upstream) || upstream.StatusCode != 403 || opened != 0 {
			t.Fatalf("ineligible transport lost stateless execution: eligible=%v opens=%d err=%v", eligible, opened, err)
		}
		p.Shutdown()
	}
}
