package cursor

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
)

func TestModelLabelsAreNeutralAcrossFamilies(t *testing.T) {
	for _, tc := range []struct{ id, label, want string }{
		{"claude-fable-5-1-low", "Claude Fable 5.1 1M Low (NO ZDR)", "Claude Fable 5.1 1M (NO ZDR) (Cursor)"},
		{"claude-opus-5-thinking-high", "Claude Opus 5 1M Thinking", "Claude Opus 5 1M (Cursor)"},
		{"gemini-3.6-flash-minimal", "Gemini 3.6 Flash Minimal", "Gemini 3.6 Flash (Cursor)"},
		{"gpt-5.6-luna-high", "GPT-5.6 Luna 1M High", "GPT-5.6 Luna 1M (Cursor)"},
		{"gpt-5.5-extra-high", "GPT-5.5 1M Extra High", "GPT-5.5 1M (Cursor)"},
		{"gpt-5.4-mini-none", "GPT-5.4 Mini None", "GPT-5.4 Mini (Cursor)"},
		{"grok-4.7-low", "Grok 4.7  Low", "Grok 4.7 (Cursor)"},
		{"kimi-k3-low", "Kimi K3 Low", "Kimi K3 (Cursor)"},
		{"muse-wonder-1.0-minimal", "Muse Wonder  Minimal", "Muse Wonder (Cursor)"},
		{"grok-4.7-high-fast", "Grok 4.7 High Fast", "Grok 4.7 (Fast) (Cursor)"},
		{"model-fast-low", "Model Low (Fast)", "Model (Fast) (Cursor)"},
		{"model-low", "Model Low (Cursor)", "Model (Cursor)"},
		{"model-low", "Model (Low) [NO ZDR]", "Model [NO ZDR] (Cursor)"},
		{"model", "High Fidelity", "High Fidelity (Cursor)"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			e := NewExecutor(DefaultConfig())
			defer e.Close()
			models := discoverMetadata(t, e, "account", "", []*pb.ModelDetails{{ModelId: tc.id, DisplayName: tc.label}}, nil)
			if len(models) != 1 || models[0].DisplayName != tc.want {
				t.Fatalf("label=%q want=%q", models[0].DisplayName, tc.want)
			}
		})
	}
}

func TestModelLabelsDoNotDependOnDiscoveryOrder(t *testing.T) {
	for _, rows := range [][]*pb.ModelDetails{
		{{ModelId: "model-low", DisplayName: "Model Low"}, {ModelId: "model-high", DisplayName: "Model High"}},
		{{ModelId: "model-high", DisplayName: "Model High"}, {ModelId: "model-low", DisplayName: "Model Low"}},
	} {
		if group := groupVariants(rows)[0]; group.display != "Model (Cursor)" {
			t.Fatalf("discovery order leaked into label: %q", group.display)
		}
	}
}

func TestFastModelsKeepSeparateIDsEffortsAndRouting(t *testing.T) {
	rows := []*pb.ModelDetails{
		{ModelId: "grok-4.7-low", DisplayName: "Grok 4.7 Low"},
		{ModelId: "grok-4.7-high", DisplayName: "Grok 4.7 High"},
		{ModelId: "grok-4.7-low-fast", DisplayName: "Grok 4.7 Low Fast"},
		{ModelId: "grok-4.7-high-fast", DisplayName: "Grok 4.7 High Fast"},
	}
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	models := discoverMetadata(t, e, "account", "", rows, nil)
	if len(models) != 2 || models[0].ID != "grok-4.7" || models[1].ID != "grok-4.7-fast" {
		t.Fatalf("fast tier collapsed: %v", models)
	}
	for _, model := range models {
		if !reflect.DeepEqual(model.Thinking.Levels, []string{"low", "high"}) {
			t.Fatalf("tier effort controls=%v", model.Thinking.Levels)
		}
		for _, effort := range []string{"low", "high"} {
			req := pluginapi.ExecutorRequest{Model: model.ID + "(" + effort + ")", StorageJSON: []byte(`{"type":"cursor","id":"fixture","access_token":"fixture-token"}`)}
			got, _, err := e.resolveExecutionModel(context.Background(), req)
			want := "grok-4.7-" + effort
			if strings.HasSuffix(model.ID, "-fast") {
				want += "-fast"
			}
			if err != nil || got != want {
				t.Fatalf("route=%s got=%s want=%s error=%v", req.Model, got, want, err)
			}
		}
	}
}

func TestExtraHighIsAnEffortNotAnotherModel(t *testing.T) {
	groups := groupVariants(variantRows("gpt-5.5-low", "gpt-5.5-extra-high", "gpt-5.5-extra-high-fast"))
	if len(groups) != 2 || groups[0].base != "gpt-5.5" || groups[0].ids["xhigh"] != "gpt-5.5-extra-high" || groups[1].base != "gpt-5.5-fast" || groups[1].ids["xhigh"] != "gpt-5.5-extra-high-fast" {
		t.Fatalf("extra-high split the model family: %v", groups)
	}
}

func TestFastTierUsesItsOwnVerifiedMetadata(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	standard := modelWireNumber(modelWireString(nil, 1, "model"), 15, 200000)
	fast := modelWireNumber(modelWireString(nil, 1, "model-fast"), 15, 100000)
	models := discoverMetadata(t, e, "account", "", variantRows("model-high", "model-high-fast"), availableFixture(standard, fast))
	if len(models) != 2 || models[0].ContextLength != 200000 || models[1].ContextLength != 100000 {
		t.Fatalf("speed-tier metadata merged: %v", models)
	}
}

func TestModelListCleansExistingVariantNamesWithoutChangingCustomNames(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	discoverMetadata(t, e, "account", "", []*pb.ModelDetails{{ModelId: "grok-4.7-low", DisplayName: "Grok 4.7 Low"}}, nil)
	response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: []byte(`{"data":[{"id":"cursor/grok-4.7","owned_by":"cursor","name":"Grok 4.7 Low","display_name":"Grok 4.7 Low"},{"id":"cursor/grok-4.7","owned_by":"cursor","name":"personal favourite","display_name":"custom"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &root); err != nil {
		t.Fatal(err)
	}
	if root.Data[0]["name"] != "Grok 4.7 (Cursor)" || root.Data[0]["display_name"] != "Grok 4.7 (Cursor)" || root.Data[1]["name"] != "personal favourite" || root.Data[1]["display_name"] != "custom" {
		t.Fatalf("model names not corrected safely: %v", root.Data)
	}
}
