package cursor

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
)

func variantRows(ids ...string) []*pb.ModelDetails {
	rows := make([]*pb.ModelDetails, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, &pb.ModelDetails{ModelId: id, DisplayName: "Model " + id})
	}
	return rows
}

func TestSplitVariant(t *testing.T) {
	for _, tc := range []struct{ id, base, kind string }{
		{"claude-opus-4-8-high", "claude-opus-4-8", "high"},
		{"gemini-3.6-flash-minimal", "gemini-3.6-flash", "minimal"},
		{"claude-4.5-sonnet-thinking", "claude-4.5-sonnet", "thinking"},
		{"claude-4.5-sonnet", "claude-4.5-sonnet", "default"},
		{"gpt-5.3-codex", "gpt-5.3-codex", "default"},
		{"some-max-model", "some-max-model", "default"},
		{"ns/model-low", "ns/model", "low"},
		{"model-HIGH", "model", "high"},
	} {
		base, kind := splitVariant(tc.id)
		if base != tc.base || kind != tc.kind {
			t.Errorf("%s: got (%s, %s) want (%s, %s)", tc.id, base, kind, tc.base, tc.kind)
		}
	}
}

func TestGroupVariantsCollapse(t *testing.T) {
	rows := []*pb.ModelDetails{
		{ModelId: "claude-opus-4-8-low", DisplayName: "Claude Opus 4.8"},
		{ModelId: "claude-opus-4-8-high", DisplayName: "Claude Opus 4.8"},
		{ModelId: "claude-4.5-sonnet", DisplayName: "Claude Sonnet 4.5"},
		{ModelId: "claude-4.5-sonnet-thinking", DisplayName: "Claude Sonnet 4.5"},
		{ModelId: "gemini-3.1-pro", DisplayName: "Gemini 3.1 Pro"},
	}
	groups := groupVariants(rows)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(groups))
	}
	if groups[0].base != "claude-opus-4-8" || len(groups[0].ids) != 2 {
		t.Fatalf("opus group: %#v", groups[0])
	}
	if groups[1].ids["default"] != "claude-4.5-sonnet" || groups[1].ids["thinking"] != "claude-4.5-sonnet-thinking" {
		t.Fatalf("sonnet group: %#v", groups[1])
	}
	if groups[2].base != "gemini-3.1-pro" || len(advertisedLevels(groups[2])) != 0 {
		t.Fatalf("singleton should not advertise levels: %#v", groups[2])
	}
	if len(advertisedLevels(groups[0])) != 6 {
		t.Fatalf("effort group should advertise six levels: %#v", groups[0])
	}
}

func TestGroupVariantsMismatchedBaseStaysSeparate(t *testing.T) {
	rows := []*pb.ModelDetails{
		{ModelId: "alpha-1", DisplayName: "Shared"},
		{ModelId: "beta-2", DisplayName: "Shared"},
	}
	groups := groupVariants(rows)
	if len(groups) != 2 || groups[0].base != "alpha-1" || groups[1].base != "beta-2" {
		t.Fatalf("mismatched bases must not collapse: %#v", groups)
	}
}

func TestResolveVariant(t *testing.T) {
	group := &variantGroup{base: "claude-opus-4-8", ids: map[string]string{
		"low": "claude-opus-4-8-low", "medium": "claude-opus-4-8-medium",
		"high": "claude-opus-4-8-high", "max": "claude-opus-4-8-max",
	}}
	for _, tc := range []struct{ effort, want string }{
		{"", "claude-opus-4-8-medium"},
		{"low", "claude-opus-4-8-low"},
		{"medium", "claude-opus-4-8-medium"},
		{"high", "claude-opus-4-8-high"},
		{"xhigh", "claude-opus-4-8-max"},
		{"max", "claude-opus-4-8-max"},
		{"minimal", "claude-opus-4-8-medium"},
		{"bogus", "claude-opus-4-8-medium"},
	} {
		got, err := resolveVariant(group, tc.effort)
		if err != nil || got != tc.want {
			t.Errorf("effort %q: got %q err %v, want %q", tc.effort, got, err, tc.want)
		}
	}
	thinking := &variantGroup{base: "claude-4.5-sonnet", ids: map[string]string{
		"default": "claude-4.5-sonnet", "thinking": "claude-4.5-sonnet-thinking",
	}}
	for _, tc := range []struct{ effort, want string }{
		{"", "claude-4.5-sonnet"},
		{"minimal", "claude-4.5-sonnet"},
		{"high", "claude-4.5-sonnet-thinking"},
		{"max", "claude-4.5-sonnet-thinking"},
	} {
		got, err := resolveVariant(thinking, tc.effort)
		if err != nil || got != tc.want {
			t.Errorf("thinking group effort %q: got %q err %v, want %q", tc.effort, got, err, tc.want)
		}
	}
	if _, err := resolveVariant(nil, "high"); err == nil {
		t.Fatal("nil group accepted")
	}
}

func TestRequestEffortPrecedence(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"low"}`)
	original := []byte(`{"model":"claude-opus-4-8","reasoning_effort":"max"}`)
	req := pluginapi.ExecutorRequest{Model: "claude-opus-4-8(high)", Format: "chat-completions", Payload: payload, OriginalRequest: original, SourceFormat: "openai"}
	if got := requestEffort(req); got != "high" {
		t.Fatalf("suffix should win: %q", got)
	}
	req.Model = "claude-opus-4-8"
	if got := requestEffort(req); got != "low" {
		t.Fatalf("payload should win: %q", got)
	}
	req.Payload = []byte(`{"model":"claude-opus-4-8","messages":[]}`)
	if got := requestEffort(req); got != "max" {
		t.Fatalf("original should win: %q", got)
	}
	req.Model = "claude-opus-4-8(none)"
	if got := requestEffort(req); got != "" {
		t.Fatalf("explicit none should select default: %q", got)
	}
	req = pluginapi.ExecutorRequest{Model: "claude-opus-4-8(bogus)", Payload: payload}
	if got := requestEffort(req); got != "low" {
		t.Fatalf("invalid suffix should fall through to payload: %q", got)
	}
}

func TestOriginalEffortFormats(t *testing.T) {
	for _, tc := range []struct {
		format, body, want string
	}{
		{"anthropic", `{"effort":"high"}`, "high"},
		{"claude", `{"thinking":{"type":"enabled"}}`, "thinking"},
		{"claude", `{"thinking":{"type":"disabled"}}`, ""},
		{"gemini", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"minimal"}}}`, "minimal"},
		{"codex", `{"reasoning":{"effort":"xhigh"}}`, "xhigh"},
		{"openai", `{"reasoning_effort":"medium"}`, "medium"},
		{"openai", `{"reasoning_effort":"ultra"}`, ""},
		{"unknown", `{"reasoning_effort":"high"}`, "high"},
		{"openai", `broken`, ""},
	} {
		req := pluginapi.ExecutorRequest{Model: "m", OriginalRequest: []byte(tc.body), SourceFormat: tc.format}
		if got := requestEffort(req); got != tc.want {
			t.Errorf("%s %s: got %q want %q", tc.format, tc.body, got, tc.want)
		}
	}
}

func warmedExecutor(t *testing.T, table *variantTable) (*Executor, pluginapi.ExecutorRequest) {
	t.Helper()
	e := NewExecutor(DefaultConfig())
	storage := []byte(`{"type":"cursor","id":"test-auth","access_token":"test-token"}`)
	e.variants = map[string]variantCacheEntry{
		variantCacheKey("test-token"): {table: table, expires: time.Now().Add(variantCacheTTL)},
	}
	return e, pluginapi.ExecutorRequest{Model: "claude-opus-4-8", StorageJSON: storage}
}

func TestResolveExecutionModel(t *testing.T) {
	table := &variantTable{byBase: map[string]*variantGroup{
		"claude-opus-4-8": {base: "claude-opus-4-8", display: "Claude Opus 4.8", ids: map[string]string{
			"low": "claude-opus-4-8-low", "medium": "claude-opus-4-8-medium", "high": "claude-opus-4-8-high",
		}},
	}}
	e, req := warmedExecutor(t, table)
	got, err := e.resolveExecutionModel(context.Background(), req)
	if err != nil || got != "claude-opus-4-8-medium" {
		t.Fatalf("default: %q %v", got, err)
	}
	req.Model = "claude-opus-4-8(low)"
	if got, err := e.resolveExecutionModel(context.Background(), req); err != nil || got != "claude-opus-4-8-low" {
		t.Fatalf("suffix: %q %v", got, err)
	}
	req.Model = "claude-opus-4-8-high"
	if _, err := e.resolveExecutionModel(context.Background(), req); err == nil {
		t.Fatal("legacy suffixed ID should explain the migration")
	}
	req.Model = "no-such-model"
	if _, err := e.resolveExecutionModel(context.Background(), req); err == nil {
		t.Fatal("unknown model accepted")
	}
}
