package cursor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"google.golang.org/protobuf/encoding/protowire"
)

func contextVariantFixture(slug, context string, maxMode bool) []byte {
	variant := modelWireString(nil, 11, slug)
	if maxMode {
		variant = modelWireNumber(variant, 3, 1)
	}
	if context != "" {
		parameter := modelWireString(modelWireString(nil, 1, "context"), 2, context)
		variant = protowire.AppendBytes(protowire.AppendTag(variant, 1, protowire.BytesType), parameter)
	}
	return variant
}

func contextParentFixture(name string, normal, max uint64, variants ...[]byte) []byte {
	parent := modelWireString(nil, 1, name)
	if normal > 0 {
		parent = modelWireNumber(parent, 15, normal)
	}
	if max > 0 {
		parent = modelWireNumber(parent, 16, max)
	}
	for _, variant := range variants {
		parent = protowire.AppendBytes(protowire.AppendTag(parent, 30, protowire.BytesType), variant)
	}
	return parent
}

func TestFastContextSurvivesEmptyExactPlaceholder(t *testing.T) {
	parent := contextParentFixture("model", 200000, 1000000, contextVariantFixture("model-high-fast", "272k", false))
	placeholder := modelWireString(modelWireString(nil, 1, "model-high-fast"), 18, "model-high-fast")
	for _, rows := range [][][]byte{{parent, placeholder}, {placeholder, parent}} {
		e := NewExecutor(DefaultConfig())
		models := discoverMetadata(t, e, "account", "", variantRows("model-high-fast"), availableFixture(rows...))
		e.Close()
		if len(models) != 1 || models[0].ContextLength != 272000 || models[0].InputTokenLimit != 272000 {
			t.Fatalf("explicit fast context lost to placeholder: %+v", models)
		}
	}
}

func TestFastContextUsesConservativeExplicitVariantAssociation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		variants [][]byte
		want     int64
	}{
		{"repeated mode", [][]byte{contextVariantFixture("model-high-fast", "272k", false), contextVariantFixture("model-high-fast", "1m", true)}, 272000},
		{"same mode", [][]byte{contextVariantFixture("model-high-fast", "300k", true), contextVariantFixture("model-high-fast", "1m", true)}, 300000},
		{"same context", [][]byte{contextVariantFixture("model-high-fast", "272k", false), contextVariantFixture("model-high-fast", "272k", false)}, 272000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				variants := append([][]byte(nil), tc.variants...)
				if reverse {
					variants[0], variants[1] = variants[1], variants[0]
				}
				e := NewExecutor(DefaultConfig())
				models := discoverMetadata(t, e, "account", "", variantRows("model-high-fast"), availableFixture(contextParentFixture("model", 200000, 1000000, variants...)))
				e.Close()
				if models[0].ContextLength != tc.want {
					t.Fatalf("context=%d want=%d", models[0].ContextLength, tc.want)
				}
			}
		})
	}
}

func TestFastContextKeepsExactLimitsAndUnknownAssociations(t *testing.T) {
	parent := contextParentFixture("model", 200000, 0, contextVariantFixture("model-high-fast", "", false))
	exact := modelWireNumber(modelWireString(nil, 1, "model-high-fast"), 15, 100000)
	for _, rows := range [][][]byte{{parent, exact}, {exact, parent}} {
		e := NewExecutor(DefaultConfig())
		models := discoverMetadata(t, e, "account", "", variantRows("model-high-fast"), availableFixture(rows...))
		e.Close()
		if models[0].ContextLength != 100000 {
			t.Fatalf("exact fast limit lost: %+v", models[0])
		}
	}
	for _, metadata := range [][]byte{
		availableFixture(contextParentFixture("model", 200000, 0)),
		availableFixture(contextParentFixture("model", 200000, 0, contextVariantFixture("model-high-fast", "", true))),
		availableFixture(parent, contextParentFixture("other", 400000, 0, contextVariantFixture("model-high-fast", "", false))),
		availableFixture(parent, modelWireNumber(modelWireString(nil, 1, "model-high-fast"), 15, 0)),
	} {
		e := NewExecutor(DefaultConfig())
		models := discoverMetadata(t, e, "account", "", variantRows("model-high-fast"), metadata)
		e.Close()
		if models[0].ContextLength != 0 {
			t.Fatalf("unverified fast limit invented: %+v", models[0])
		}
	}
}

func TestFastContextLeavesUnresolvedEffortsUnknown(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	models := discoverMetadata(t, e, "account", "", variantRows("model-low-fast", "model-high-fast"), availableFixture(contextParentFixture("model", 200000, 0, contextVariantFixture("model-high-fast", "272k", false))))
	if models[0].ContextLength != 0 {
		t.Fatalf("unresolved effort inherited context: %+v", models[0])
	}
}

func TestFastContextParameterValidation(t *testing.T) {
	for _, value := range []string{"", "0k", "-1k", "1.5m", "999999999999m", "200000", "unknown"} {
		e := NewExecutor(DefaultConfig())
		variant := contextVariantFixture("model-high-fast", value, false)
		if value == "" {
			parameter := modelWireString(modelWireString(nil, 1, "context"), 2, "")
			variant = protowire.AppendBytes(protowire.AppendTag(variant, 1, protowire.BytesType), parameter)
		}
		models := discoverMetadata(t, e, "account", "", variantRows("model-high-fast"), availableFixture(contextParentFixture("model", 200000, 0, variant)))
		e.Close()
		if models[0].ContextLength != 0 {
			t.Fatalf("invalid context %q advertised %d", value, models[0].ContextLength)
		}
	}
}

func TestContextAliasWithoutLimitsKeepsCrossModelAmbiguity(t *testing.T) {
	known := modelWireString(contextParentFixture("one", 200000, 0), 37, "shared")
	unknown := modelWireString(modelWireString(nil, 1, "two"), 37, "shared")
	for _, rows := range [][][]byte{{known, unknown}, {unknown, known}} {
		metadata, err := availableModelInfo(availableFixture(rows...))
		if err != nil {
			t.Fatal(err)
		}
		if metadata["shared"] != nil {
			t.Fatalf("ambiguous partial alias restored metadata: %+v", metadata["shared"])
		}
	}
	parent := contextParentFixture("model", 200000, 0, contextVariantFixture("model-high-fast", "272k", false))
	placeholder := modelWireString(modelWireString(nil, 1, "placeholder"), 37, "model-high-fast")
	metadata, err := availableModelInfo(availableFixture(parent, placeholder))
	if err != nil || metadata["model-high-fast"] == nil || metadata["model-high-fast"].context != 272000 {
		t.Fatalf("generic placeholder erased explicit association: %v %v", metadata, err)
	}
}

func TestFastContextReachesOpenAIAndCodexLists(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	discoverMetadata(t, e, "account", "", variantRows("model-high-fast"), availableFixture(contextParentFixture("model", 200000, 0, contextVariantFixture("model-high-fast", "272k", false)), modelWireString(nil, 1, "model-high-fast")))
	for _, tc := range []struct{ body, list, key string }{
		{`{"data":[{"id":"cursor/model-fast","owned_by":"cursor"}]}`, "data", "context_length"},
		{`{"models":[{"slug":"cursor/model-fast"}]}`, "models", "context_window"},
	} {
		response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: []byte(tc.body)})
		if err != nil {
			t.Fatal(err)
		}
		var root map[string][]map[string]json.RawMessage
		if json.Unmarshal(response.Body, &root) != nil {
			t.Fatal("invalid intercepted catalogue")
		}
		var got int64
		_ = json.Unmarshal(root[tc.list][0][tc.key], &got)
		if got != 272000 {
			t.Fatalf("%s limit=%d", tc.list, got)
		}
	}
}
