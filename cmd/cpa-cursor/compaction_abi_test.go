//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
)

func TestCompactionABIStatusSurvivesBothExecutionModes(t *testing.T) {
	abiLifecycleMu.Lock()
	drainDispatch()
	plugin.Shutdown()
	plugin = NewPlugin()
	plugin.executor.Log = nil
	resumeDispatch()
	abiLifecycleMu.Unlock()
	old := callHost
	t.Cleanup(func() { _, _ = handleMethod(pluginabi.MethodPluginShutdown, nil); callHost = old })
	catalogue, _ := proto.Marshal(&pb.GetUsableModelsResponse{Models: []*pb.ModelDetails{{ModelId: "fixture"}}})
	for _, method := range []string{pluginabi.MethodExecutorExecute, pluginabi.MethodExecutorExecuteStream} {
		for _, status := range []int{403, 429} {
			callHost = func(callback string, _ any) (json.RawMessage, error) {
				switch callback {
				case pluginabi.MethodHostHTTPOperationOpen:
					return json.RawMessage(`{"operation_id":"fixture-op"}`), nil
				case pluginabi.MethodHostHTTPDo:
					return json.Marshal(pluginapi.HTTPResponse{StatusCode: 200, Body: catalogue})
				case pluginabi.MethodHostHTTPDoStream:
					return json.Marshal(map[string]any{"status_code": status, "stream_id": "fixture-stream"})
				case pluginabi.MethodHostHTTPStreamRead:
					return json.RawMessage(`{"done":true}`), nil
				case pluginabi.MethodHostHTTPStreamClose, pluginabi.MethodHostHTTPCancel:
					return json.RawMessage(`{}`), nil
				default:
					t.Errorf("unexpected host method %s", callback)
					return json.RawMessage(`{}`), nil
				}
			}
			req := pluginapi.ExecutorRequest{Model: "fixture", Format: "chat-completions", StorageJSON: []byte(`{"type":"cursor","id":"fixture","access_token":"fixture-secret"}`), Payload: []byte(`{"messages":[{"role":"user","content":"old task"},{"role":"assistant","content":"old-model answer"},{"role":"user","content":"summarise this conversation"}]}`)}
			raw, _ := json.Marshal(req)
			var wire map[string]any
			_ = json.Unmarshal(raw, &wire)
			wire["stream_id"] = "outbound-stream"
			raw, _ = json.Marshal(wire)
			response, err := handleMethod(method, raw)
			if err != nil {
				t.Fatal(err)
			}
			var envelope pluginabi.Envelope
			if json.Unmarshal(response, &envelope) != nil || envelope.OK || envelope.Error == nil || envelope.Error.HTTPStatus != status || strings.TrimSpace(envelope.Error.Message) == "" {
				t.Fatalf("compaction status lost: %s", response)
			}
			if envelope.Error.Retryable != (status == 429) || strings.Contains(string(response), "fixture-secret") {
				t.Fatalf("compaction retry/redaction broken: %s", response)
			}
			plugin.hostWorkers.Wait()
		}
	}
}
