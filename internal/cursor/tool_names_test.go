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
		{"lookup", "cpa-mcp_cpa_lookup", "mcp_cpa_lookup"},
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
				for _, alias := range []string{wire, "mcp_cpa_" + wire, "cpa-" + wire, "cpa-mcp_cpa_" + wire} {
					if got := built.resolve[alias]; got != name {
						t.Errorf("tool %q alias %q resolves to %q", name, alias, got)
					}
				}
			}
		})
	}
}

func TestTaskToolUsesCursorSafeBridgeName(t *testing.T) {
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":"delegate"}],"tools":[{"type":"function","function":{"name":"task","parameters":{"type":"object"}}}]}`)
	built, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if built.wire["task"] != "client_task" {
		t.Fatalf("task wire name = %q, want client_task", built.wire["task"])
	}
	if built.resolve["mcp_cpa_client_task"] != "task" {
		t.Fatal("bridged task name did not resolve to the client task tool")
	}
	if built.tools[0].ToolName != "mcp_cpa_client_task" {
		t.Fatalf("advertised task name = %q", built.tools[0].ToolName)
	}
}
