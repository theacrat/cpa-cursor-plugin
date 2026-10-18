package cursor

import (
	"encoding/json"
	"testing"
)

func TestToolNamesAndAliasesCannotIdentifyDifferentFunctions(t *testing.T) {
	for _, names := range [][]string{
		{"lookup", "cpa-lookup"},
		{"cpa-lookup", "lookup"},
		{"lookup", "mcp_cpa_lookup", "mcp_cpa_mcp_cpa_lookup"},
		{"lookup", "cpa-lookup", "mcp_cpa_lookup", "lookup_2", "cpa-lookup_2"},
	} {
		t.Run(names[0]+names[len(names)-1], func(t *testing.T) {
			tools := make([]map[string]any, 0, len(names))
			for _, name := range names {
				tools = append(tools, map[string]any{"type": "function", "function": map[string]string{"name": name}})
			}
			req := testRequest()
			req.Payload, _ = json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}, "tools": tools})
			built, err := buildRequest(req)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range names {
				wire := built.wire[name]
				for _, alias := range []string{wire, "mcp_cpa_" + wire, "cpa-" + wire} {
					if got := built.resolve[alias]; got != name {
						t.Errorf("tool %q alias %q resolves to %q", name, alias, got)
					}
				}
			}
		})
	}
}
