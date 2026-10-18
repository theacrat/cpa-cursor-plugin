package cursor

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func modelWireString(raw []byte, number protowire.Number, value string) []byte {
	return protowire.AppendString(protowire.AppendTag(raw, number, protowire.BytesType), value)
}

func modelWireNumber(raw []byte, number protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(raw, number, protowire.VarintType), value)
}

func availableFixture(models ...[]byte) []byte {
	var raw []byte
	for _, model := range models {
		raw = protowire.AppendBytes(protowire.AppendTag(raw, 2, protowire.BytesType), model)
	}
	return raw
}

func discoverMetadata(t *testing.T, e *Executor, authID, prefix string, rows []*pb.ModelDetails, metadata []byte) []pluginapi.ModelInfo {
	t.Helper()
	usable, err := proto.Marshal(&pb.GetUsableModelsResponse{Models: rows})
	if err != nil {
		t.Fatal(err)
	}
	storage := []byte(`{"type":"cursor","id":"fixture","access_token":"fixture-token"}`)
	if prefix != "" {
		storage = []byte(`{"type":"cursor","id":"fixture","access_token":"fixture-token","prefix":"` + prefix + `"}`)
	}
	req := pluginapi.AuthModelRequest{AuthID: authID, Host: pluginapi.HostConfigSummary{ForceModelPrefix: true}, StorageJSON: storage}
	req.HTTPClient = fixtureHost{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		if strings.HasSuffix(req.URL, "/AvailableModels") {
			if metadata == nil {
				return pluginapi.HTTPResponse{StatusCode: 404}, nil
			}
			return pluginapi.HTTPResponse{StatusCode: 200, Body: metadata}, nil
		}
		return pluginapi.HTTPResponse{StatusCode: 200, Body: usable}, nil
	}}
	response, err := e.ModelsForAuth(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return response.Models
}

func TestDiscoveryMatchesVerifiedFamilyMetadata(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	model := modelWireString(nil, 1, "model")
	model = modelWireNumber(model, 15, 200000)
	model = modelWireNumber(model, 16, 1000000)
	model = modelWireNumber(model, 10, 1)
	model = modelWireNumber(model, 5, 1)
	models := discoverMetadata(t, e, "account", "", variantRows("model-low", "model-high"), availableFixture(model))
	if len(models) != 1 {
		t.Fatalf("models=%v", models)
	}
	got := models[0]
	if got.ID != "model" || got.OwnedBy != "cursor" || got.ContextLength != 200000 || got.InputTokenLimit != 200000 || !reflect.DeepEqual(got.SupportedInputModalities, []string{"text", "image"}) {
		t.Fatalf("verified metadata lost: %+v", got)
	}
	if got.OutputTokenLimit != 0 || got.MaxCompletionTokens != 0 {
		t.Fatal("invented an output token limit")
	}
	if !reflect.DeepEqual(got.SupportedParameters, []string{"tools", "tool_choice", "reasoning_effort"}) {
		t.Fatalf("supported parameters=%v", got.SupportedParameters)
	}
	if got.Thinking == nil || !reflect.DeepEqual(got.Thinking.Levels, []string{"low", "high"}) {
		t.Fatalf("reasoning advertised undiscovered levels: %v", got.Thinking)
	}
}

func TestDiscoveryKeepsUnknownMetadataAbsent(t *testing.T) {
	for _, metadata := range [][]byte{nil, availableFixture(modelWireString(nil, 1, "model")), []byte{0xff}} {
		e := NewExecutor(DefaultConfig())
		models := discoverMetadata(t, e, "account", "", variantRows("model"), metadata)
		e.Close()
		got := models[0]
		if got.ContextLength != 0 || got.OutputTokenLimit != 0 || got.MaxCompletionTokens != 0 || len(got.SupportedParameters) != 0 || !reflect.DeepEqual(got.SupportedInputModalities, []string{"text"}) {
			t.Fatalf("unknown metadata fabricated: %+v", got)
		}
	}
}

func TestDiscoveryUsesExactAliasesAndConservativeVariantLimits(t *testing.T) {
	low := modelWireNumber(modelWireString(nil, 1, "unrelated-low-name"), 15, 200000)
	low = modelWireString(low, 37, "model-low")
	low = modelWireNumber(low, 10, 1)
	high := modelWireNumber(modelWireString(nil, 1, "unrelated-high-name"), 15, 100000)
	high = modelWireString(high, 18, "model-high")
	high = modelWireNumber(high, 10, 0)
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	models := discoverMetadata(t, e, "account", "", variantRows("model-low", "model-high"), availableFixture(low, high))
	if got := models[0]; got.ContextLength != 100000 || !reflect.DeepEqual(got.SupportedInputModalities, []string{"text"}) {
		t.Fatalf("unsafe variant metadata: %+v", got)
	}
}

func TestAvailableMetadataRejectsInvalidValuesAndAmbiguousAliases(t *testing.T) {
	for _, raw := range [][]byte{
		availableFixture(modelWireNumber(modelWireString(nil, 1, "model"), 10, 2)),
		availableFixture(modelWireNumber(modelWireString(nil, 1, "model"), 15, 1<<63)),
		availableFixture(modelWireString(modelWireString(nil, 1, "model"), 15, "bad")),
	} {
		if _, err := availableModelInfo(raw); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	first := modelWireString(modelWireNumber(modelWireString(nil, 1, "one"), 15, 100000), 37, "shared")
	second := modelWireString(modelWireNumber(modelWireString(nil, 1, "two"), 15, 200000), 37, "shared")
	metadata, err := availableModelInfo(availableFixture(first, second))
	if err != nil || metadata["shared"] != nil || metadata["one"].context != 100000 {
		t.Fatalf("ambiguous metadata matched: %v %v", metadata, err)
	}
	exact := modelWireNumber(modelWireString(nil, 1, "shared"), 15, 300000)
	metadata, err = availableModelInfo(availableFixture(first, second, exact))
	if err != nil || metadata["shared"] == nil || metadata["shared"].context != 300000 {
		t.Fatalf("exact metadata lost to ambiguous aliases: %v", err)
	}
}

func TestDiscoveryPreservesExactVariantAndUnknownLimits(t *testing.T) {
	base := modelWireNumber(modelWireString(nil, 1, "model"), 15, 200000)
	variant := modelWireString(nil, 11, "model-high")
	variant = modelWireNumber(variant, 3, 1)
	base = protowire.AppendBytes(protowire.AppendTag(base, 30, protowire.BytesType), variant)
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	models := discoverMetadata(t, e, "account", "", variantRows("model-low", "model-high"), availableFixture(base))
	if models[0].ContextLength != 0 {
		t.Fatal("max-mode variant inherited an unknown context limit")
	}
}

func TestThinkingToggleUsesHostSupportedEffort(t *testing.T) {
	groups := groupVariants(variantRows("model", "model-thinking"))
	if levels := advertisedLevels(groups[0]); !reflect.DeepEqual(levels, []string{"high"}) {
		t.Fatalf("toggle advertised unusable host levels: %v", levels)
	}
	if id, err := resolveVariant(groups[0], "high"); err != nil || id != "model-thinking" {
		t.Fatalf("toggle metadata does not match execution: %s %v", id, err)
	}
}

func TestUncontrolledThinkingHasNoInventedControls(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	models := discoverMetadata(t, e, "account", "", []*pb.ModelDetails{{ModelId: "model", ThinkingDetails: &pb.ThinkingDetails{}}}, nil)
	if models[0].Thinking == nil || len(models[0].Thinking.Levels) != 0 || models[0].Thinking.ZeroAllowed || len(models[0].SupportedParameters) != 0 {
		t.Fatalf("invented thinking controls: %+v", models[0])
	}
}

func TestDefaultEffortDoesNotAdvertiseDisabledReasoning(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	models := discoverMetadata(t, e, "account", "", variantRows("model", "model-low", "model-high"), nil)
	if models[0].Thinking == nil || models[0].Thinking.ZeroAllowed {
		t.Fatal("default effort was advertised as disabled thinking")
	}
}
