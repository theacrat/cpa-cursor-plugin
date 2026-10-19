package cursor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
)

func TestModelListLabelsOnlyQualifyDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"unique", `{"data":[{"id":"cursor/model","owned_by":"cursor","name":"Model 1M Low (NO ZDR) (Cursor)"}]}`, []string{"Model"}},
		{"unique fast", `{"data":[{"id":"cursor/model-fast","owned_by":"cursor"}]}`, []string{"Model"}},
		{"speed tiers", `{"data":[{"id":"cursor/model","owned_by":"cursor"},{"id":"cursor/model-fast","owned_by":"cursor"}]}`, []string{"Model", "Model (Fast)"}},
		{"another provider", `{"data":[{"id":"cursor/model","owned_by":"cursor"},{"id":"native/model","owned_by":"native","name":"Model"}]}`, []string{"Model (cursor)", "Model"}},
		{"display name collision", `{"data":[{"id":"cursor/model","owned_by":"cursor"},{"id":"native/other","owned_by":"native","display_name":"Model"}]}`, []string{"Model (cursor)", ""}},
		{"normalised collision", `{"data":[{"id":"cursor/model","owned_by":"cursor"},{"id":"native/model","owned_by":"native","name":" model "}]}`, []string{"Model (cursor)", " model "}},
		{"provider without label", `{"data":[{"id":"cursor/model","owned_by":"cursor"},{"id":"native/model","owned_by":"native"}]}`, []string{"Model (cursor)", ""}},
		{"provider and tiers", `{"data":[{"id":"cursor/model","owned_by":"cursor"},{"id":"cursor/model-fast","owned_by":"cursor"},{"id":"native/model","owned_by":"native","name":"Model"}]}`, []string{"Model (cursor)", "Model (Fast)", "Model"}},
		{"fast provider collision", `{"data":[{"id":"cursor/model","owned_by":"cursor"},{"id":"cursor/model-fast","owned_by":"cursor"},{"id":"native/fast","owned_by":"native","name":"Model (Fast)"}]}`, []string{"Model", "Model (Fast) (cursor)", "Model (Fast)"}},
		{"legacy provider casing", `{"data":[{"id":"cursor/model","owned_by":"cursor","name":"Model (Cursor)","display_name":"Model (Cursor)"},{"id":"native/model","owned_by":"native","name":"Model"}]}`, []string{"Model (cursor)", "Model"}},
		{"same model repeated", `{"data":[{"id":"cursor/model","owned_by":"cursor"},{"id":"cursor/model","owned_by":"cursor"}]}`, []string{"Model", "Model"}},
		{"custom", `{"data":[{"id":"cursor/model","owned_by":"cursor","name":"my custom 1M (NO ZDR)","display_name":"personal"}]}`, []string{"my custom 1M (NO ZDR)"}},
		{"null names", `{"data":[{"id":"cursor/model","owned_by":"cursor","name":null,"display_name":null}]}`, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewExecutor(DefaultConfig())
			defer e.Close()
			metadata := modelWireNumber(modelWireString(nil, 1, "model"), 15, 200000)
			rows := []*pb.ModelDetails{{ModelId: "model-low", DisplayName: "Model 1M Low (NO ZDR)"}, {ModelId: "model-low-fast", DisplayName: "Model 1M Low Fast (NO ZDR)"}}
			discoverMetadata(t, e, "account", "", rows, availableFixture(metadata))
			response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: []byte(tc.body)})
			if err != nil || len(response.Body) == 0 {
				t.Fatalf("no labels returned: %v", err)
			}
			var root struct {
				Data []map[string]any `json:"data"`
			}
			if err := json.Unmarshal(response.Body, &root); err != nil {
				t.Fatal(err)
			}
			for index, want := range tc.want {
				got, _ := root.Data[index]["name"].(string)
				if got != want {
					t.Fatalf("row %d name=%q want=%q", index, got, want)
				}
			}
			if root.Data[0]["context_length"] != nil && root.Data[0]["context_length"] != float64(200000) {
				t.Fatal("label cleanup changed metadata")
			}
			if again, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: response.Body}); err != nil || len(again.Body) != 0 {
				t.Fatalf("labels are not idempotent: %v", err)
			}
		})
	}
}

func TestModelListCodexLabelsOnlyQualifyVisibleDuplicates(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	rows := []*pb.ModelDetails{{ModelId: "model-low", DisplayName: "Model 1M Low (NO ZDR)"}, {ModelId: "model-low-fast", DisplayName: "Model 1M Low Fast (NO ZDR)"}}
	discoverMetadata(t, e, "account", "", rows, nil)
	for _, tc := range []struct {
		body string
		want []string
	}{
		{`{"models":[{"slug":"cursor/model-fast","display_name":"Model 1M Low (Fast) (Cursor) (NO ZDR)"}]}`, []string{"Model"}},
		{`{"models":[{"slug":"cursor/model"},{"slug":"cursor/model-fast"}]}`, []string{"Model", "Model (Fast)"}},
		{`{"models":[{"slug":"cursor/model"},{"slug":"other/model","display_name":"Model"}]}`, []string{"Model (cursor)", "Model"}},
		{`{"models":[{"slug":"cursor/model","display_name":"Model (Cursor)"},{"slug":"other/model","display_name":"Model"}]}`, []string{"Model (cursor)", "Model"}},
	} {
		response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: []byte(tc.body)})
		if err != nil {
			t.Fatal(err)
		}
		var root struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(response.Body, &root); err != nil {
			t.Fatal(err)
		}
		for index, want := range tc.want {
			if root.Models[index]["display_name"] != want {
				t.Fatalf("Codex label=%v want=%s", root.Models[index]["display_name"], want)
			}
		}
	}
}

func TestModelListDropsDisambiguatorsAfterDuplicateDisappears(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	discoverMetadata(t, e, "account", "", []*pb.ModelDetails{{ModelId: "alpha", DisplayName: "Model"}, {ModelId: "beta", DisplayName: "Model"}}, nil)
	response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: []byte(`{"data":[{"id":"cursor/alpha","owned_by":"cursor"},{"id":"cursor/beta","owned_by":"cursor"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body, &root); err != nil {
		t.Fatal(err)
	}
	if root.Data[0]["name"] == root.Data[1]["name"] {
		t.Fatal("duplicate model labels remain ambiguous")
	}
	if repeated, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: response.Body}); err != nil || len(repeated.Body) != 0 {
		t.Fatalf("route qualifiers are not idempotent: %v", err)
	}
	remaining, _ := json.Marshal(map[string]any{"data": root.Data[:1]})
	response, err = e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: remaining})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response.Body, &root); err != nil {
		t.Fatal(err)
	}
	if root.Data[0]["name"] != "Model" || root.Data[0]["display_name"] != "Model" {
		t.Fatalf("obsolete duplicate qualifiers retained: %v", root.Data[0])
	}
}

func TestModelListDropsDiscoveryQualifiersAfterDuplicateDisappears(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, first, second, qualified string
		index                                  int
	}{
		{name: "model ID", first: "alpha", second: "beta", qualified: "Model [alpha]"},
		{name: "account prefix", prefix: "work", first: "alpha", second: "beta", qualified: "Model [alpha]"},
		{name: "effort suffix", first: "high", second: "low", qualified: "Model [high]"},
		{name: "upstream namespace", first: "vendor/high", second: "other/high", qualified: "Model [other/high]", index: 1},
	} {
		for _, schema := range []struct{ list, id string }{{"data", "id"}, {"models", "slug"}} {
			t.Run(tc.name+"/"+schema.list, func(t *testing.T) {
				e := NewExecutor(DefaultConfig())
				defer e.Close()
				models := discoverMetadata(t, e, "account", tc.prefix, []*pb.ModelDetails{{ModelId: tc.first, DisplayName: "Model"}, {ModelId: tc.second, DisplayName: "Model"}}, nil)
				model := models[tc.index]
				if model.DisplayName != tc.qualified {
					t.Fatalf("discovery label=%q want=%q", model.DisplayName, tc.qualified)
				}
				id := first(tc.prefix, "cursor") + "/" + model.ID
				body, err := json.Marshal(map[string]any{schema.list: []map[string]string{{schema.id: id, "owned_by": "cursor", "name": model.DisplayName, "display_name": model.DisplayName}}})
				if err != nil {
					t.Fatal(err)
				}
				response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: body})
				if err != nil {
					t.Fatal(err)
				}
				var root map[string][]map[string]any
				if err := json.Unmarshal(response.Body, &root); err != nil {
					t.Fatal(err)
				}
				row := root[schema.list][0]
				if row["name"] != "Model" || row["display_name"] != "Model" || row[schema.id] != id {
					t.Fatalf("discovery qualifiers retained or route changed: %v", row)
				}
				if repeated, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: response.Body}); err != nil || len(repeated.Body) != 0 {
					t.Fatalf("discovery label cleanup is not idempotent: %v", err)
				}
			})
		}
	}
}

func TestModelListPreservesCustomDiscoveryLikeSuffixes(t *testing.T) {
	for _, schema := range []struct{ list, id string }{{"data", "id"}, {"models", "slug"}} {
		for _, label := range []string{"Model [personal]", "Model [beta]", "Personal [alpha]", "Model [other/alpha]", "Model [alpha] [personal]"} {
			t.Run(schema.list+"/"+label, func(t *testing.T) {
				e := NewExecutor(DefaultConfig())
				defer e.Close()
				discoverMetadata(t, e, "account", "work", []*pb.ModelDetails{{ModelId: "alpha", DisplayName: "Model"}, {ModelId: "beta", DisplayName: "Model"}}, nil)
				body, err := json.Marshal(map[string]any{schema.list: []map[string]string{{schema.id: "work/alpha", "owned_by": "cursor", "name": label, "display_name": label}}})
				if err != nil {
					t.Fatal(err)
				}
				response, err := e.InterceptResponse(context.Background(), pluginapi.ResponseInterceptRequest{Body: body})
				if err != nil {
					t.Fatal(err)
				}
				var root map[string][]map[string]any
				if err := json.Unmarshal(response.Body, &root); err != nil {
					t.Fatal(err)
				}
				row := root[schema.list][0]
				if row["name"] != label || row["display_name"] != label {
					t.Fatalf("custom suffix changed: %v", row)
				}
			})
		}
	}
}
