package cursor

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestModelListMetadataPreservesIDsAndOtherProviders(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	metadata := modelWireNumber(modelWireString(nil, 1, "model"), 15, 200000)
	metadata = modelWireNumber(metadata, 10, 1)
	metadata = modelWireNumber(metadata, 5, 1)
	discoverMetadata(t, e, "account", "work", variantRows("model-low", "model-high"), availableFixture(metadata))
	body := []byte(`{"object":"list","data":[{"id":"work/model","owned_by":"cursor","custom":7},{"id":"model","owned_by":"anthropic","context_length":123},{"id":"cursor/model","owned_by":"cursor"},{"id":"work/unknown","owned_by":"cursor"},{"id":"work/model","owned_by":"anthropic"}]}`)
	response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{StatusCode: 200, Body: body})
	if err != nil || len(response.Body) == 0 {
		t.Fatalf("no model metadata: %v", err)
	}
	var decoded struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	row := decoded.Data[0]
	if row["id"] != "work/model" || row["owned_by"] != "cursor" || row["custom"] != float64(7) || row["context_length"] != float64(200000) || row["tool_call"] != true {
		t.Fatalf("metadata or identity lost: %v", row)
	}
	if _, exists := row["max_completion_tokens"]; exists {
		t.Fatal("unknown output limit was published")
	}
	modalities := row["modalities"].(map[string]any)
	if !reflect.DeepEqual(modalities["input"], []any{"text", "image"}) || row["reasoning"] != true {
		t.Fatalf("capabilities lost: %v", row)
	}
	if len(decoded.Data) != 5 || len(decoded.Data[1]) != 3 || len(decoded.Data[2]) != 2 || len(decoded.Data[3]) != 2 || len(decoded.Data[4]) != 2 {
		t.Fatal("changed another provider or invented models")
	}
	if repeated, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: response.Body}); err != nil || len(repeated.Body) != 0 {
		t.Fatalf("metadata interceptor was not idempotent: %v", err)
	}
}

func TestModelListSnapshotBoundsAndEmptyRefresh(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	req := pluginapi.AuthModelRequest{StorageJSON: []byte(`{"type":"cursor","id":"test","access_token":"test-token"}`)}
	for index := 0; index < maxModelSnapshots+1; index++ {
		req.AuthID = string(rune('a' + index))
		e.storeModelSnapshot(req, []pluginapi.ModelInfo{{ID: "model"}})
	}
	if len(e.modelSnapshots) != maxModelSnapshots {
		t.Fatal("unbounded model metadata snapshots")
	}
	e.storeModelSnapshot(req, nil)
	if len(e.modelSnapshots[req.AuthID].models) != 0 {
		t.Fatal("empty discovery retained stale models")
	}
	e.clearModelSnapshot(req)
	if _, exists := e.modelSnapshots[req.AuthID]; exists {
		t.Fatal("unsafe host policy retained account metadata")
	}
}

func TestModelMetadataInterceptorNeverTouchesInference(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	discoverMetadata(t, e, "account", "", variantRows("model"), nil)
	body := []byte(`{"data":[{"id":"cursor/model","owned_by":"cursor"}]}`)
	for _, req := range []pluginapi.ResponseInterceptRequest{
		{Model: "model", Body: body},
		{RequestedModel: "model", Body: body},
		{Stream: true, Body: body},
		{StatusCode: 500, Body: body},
		{OriginalRequest: []byte(`{}`), Body: body},
		{RequestBody: []byte(`{}`), Body: body},
		{Metadata: map[string]any{"request_path": "/v1/chat/completions"}, Body: body},
		{Body: []byte(`{"choices":[]}`)},
		{Body: []byte(`broken`)},
	} {
		if response, err := e.InterceptResponse(context.Background(), req); err != nil || len(response.Body) != 0 {
			t.Fatalf("non-model response modified: %v", err)
		}
	}
}

func TestModelMetadataReplacesStaleAccountSnapshot(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	discoverMetadata(t, e, "account", "work", variantRows("model"), nil)
	discoverMetadata(t, e, "account", "new", variantRows("replacement"), nil)
	response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: []byte(`{"data":[{"id":"work/model","owned_by":"cursor"}]}`)})
	if err != nil || len(response.Body) != 0 {
		t.Fatalf("removed account model retained metadata: %v", err)
	}
}

func TestModelListMetadataUsesSharedAccountCapabilities(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	first := modelWireNumber(modelWireString(nil, 1, "model"), 15, 200000)
	first = modelWireNumber(first, 10, 1)
	second := modelWireNumber(modelWireString(nil, 1, "model"), 15, 100000)
	second = modelWireNumber(second, 10, 0)
	discoverMetadata(t, e, "account-one", "", variantRows("model-low", "model-high"), availableFixture(first))
	discoverMetadata(t, e, "account-two", "", variantRows("model-high"), availableFixture(second))
	model, ok := e.publishedMetadata("cursor/model")
	if !ok || model.ContextLength != 100000 || !reflect.DeepEqual(model.SupportedInputModalities, []string{"text"}) || model.Thinking == nil || !reflect.DeepEqual(model.Thinking.Levels, []string{"high"}) {
		t.Fatalf("unsafe account capabilities: %+v", model)
	}
	e.Close()
	if _, ok := e.publishedMetadata("cursor/model"); ok {
		t.Fatal("closed executor retained model metadata")
	}
}

func TestModelListCodexSchemaRetainsUnknownFields(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	discoverMetadata(t, e, "account", "", variantRows("model-high"), nil)
	response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: []byte(`{"models":[{"slug":"cursor/model","display_name":"old","picker_state":{"hidden":false}},{"slug":"native/model","display_name":"native"}]}`)})
	if err != nil || len(response.Body) == 0 {
		t.Fatalf("Codex metadata missing: %v", err)
	}
	var root struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(response.Body, &root); err != nil {
		t.Fatal(err)
	}
	if root.Models[0]["slug"] != "cursor/model" || root.Models[0]["picker_state"] == nil || root.Models[1]["display_name"] != "native" {
		t.Fatal("Codex list identity or unknown fields changed")
	}
}

func TestModelListPreservesExplicitMetadata(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	metadata := modelWireNumber(modelWireString(nil, 1, "model"), 15, 200000)
	metadata = modelWireNumber(metadata, 10, 1)
	metadata = modelWireNumber(metadata, 5, 1)
	discoverMetadata(t, e, "account", "", variantRows("model-high"), availableFixture(metadata))
	body := []byte(`{"data":[{"id":"cursor/model","owned_by":"cursor","display_name":"custom","context_length":100000,"tool_call":false,"modalities":{"input":["text"],"output":["text"]},"thinking":{"levels":[]},"reasoning":false}]}`)
	response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: body})
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &root); err != nil {
		t.Fatal(err)
	}
	row := root.Data[0]
	if row["display_name"] != "custom" || row["context_length"] != float64(100000) || row["tool_call"] != false || row["reasoning"] != false {
		t.Fatalf("explicit metadata overwritten: %v", row)
	}
	if row["supported_reasoning_levels"] != nil || row["reasoning_options"] != nil {
		t.Fatal("explicit reasoning controls were contradicted")
	}
}
