package cursor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAnalyseVariant(t *testing.T) {
	for _, tc := range []struct {
		id, base, kind string
		rank           int
		bare           bool
	}{
		{"claude-opus-4-8-high", "claude-opus-4-8", "high", 10, false},
		{"claude-opus-4-8-thinking-high-fast", "claude-opus-4-8", "high", 31, false},
		{"gemini-3.6-flash-minimal", "gemini-3.6-flash", "minimal", 10, false},
		{"claude-4.5-sonnet-thinking", "claude-4.5-sonnet", "thinking", 10, false},
		{"claude-opus-4-8-xhigh", "claude-opus-4-8", "xhigh", 10, false},
		{"claude-4.5-sonnet", "claude-4.5-sonnet", "default", 0, true},
		{"gpt-5.3-codex", "gpt-5.3-codex", "default", 0, true},
		{"gpt-5.4-mini-none", "gpt-5.4-mini", "default", 10, false},
		{"some-max-model", "some-max-model", "default", 0, true},
		{"ns/model-low", "ns/model", "low", 10, false},
		{"model-HIGH", "model", "high", 10, false},
	} {
		base, kind, rank, bare := analyseVariant(tc.id)
		if base != tc.base || kind != tc.kind || rank != tc.rank || bare != tc.bare {
			t.Errorf("%s: got (%s, %s, %d, %v) want (%s, %s, %d, %v)",
				tc.id, base, kind, rank, bare, tc.base, tc.kind, tc.rank, tc.bare)
		}
	}
}

func TestGroupVariantsCollapse(t *testing.T) {
	// Families merge whatever the display names say: the v8 list drops
	// provider display names, so only the base ID keeps rows distinct.
	rows := []*pb.ModelDetails{
		{ModelId: "claude-opus-4-8-low", DisplayName: "Claude Opus 4.8 1M Low"},
		{ModelId: "claude-opus-4-8-high", DisplayName: "Claude Opus 4.8 1M"},
		{ModelId: "claude-opus-4-8-high-fast", DisplayName: "Claude Opus 4.8 1M Fast"},
		{ModelId: "claude-opus-4-8-thinking-high", DisplayName: "Claude Opus 4.8 1M Thinking"},
		{ModelId: "claude-4.5-sonnet", DisplayName: "Claude Sonnet 4.5"},
		{ModelId: "claude-4.5-sonnet-thinking", DisplayName: "Claude Sonnet 4.5 Thinking"},
		{ModelId: "gemini-3.1-pro", DisplayName: "Gemini 3.1 Pro"},
	}
	groups := groupVariants(rows)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(groups))
	}
	opus := groups[0]
	if opus.base != "claude-opus-4-8" {
		t.Fatalf("opus group: %#v", opus)
	}
	if opus.ids["low"] != "claude-opus-4-8-low" || opus.ids["high"] != "claude-opus-4-8-high" || opus.ids["thinking"] != "" {
		t.Fatalf("opus kinds prefer standard tier: %#v", opus.ids)
	}
	if !opus.hasBare && len(advertisedLevels(opus)) != 6 {
		t.Fatalf("effort group should advertise six levels: %#v", opus)
	}
	sonnet := groups[1]
	if sonnet.ids["default"] != "claude-4.5-sonnet" || sonnet.ids["thinking"] != "claude-4.5-sonnet-thinking" || !sonnet.hasBare {
		t.Fatalf("sonnet group: %#v", sonnet)
	}
	if groups[2].base != "gemini-3.1-pro" || len(advertisedLevels(groups[2])) != 0 {
		t.Fatalf("singleton should not advertise levels: %#v", groups[2])
	}
}

func TestGroupVariantsDistinctFamiliesStaySeparate(t *testing.T) {
	rows := []*pb.ModelDetails{
		{ModelId: "alpha-1", DisplayName: "Shared"},
		{ModelId: "beta-2", DisplayName: "Shared"},
	}
	groups := groupVariants(rows)
	if len(groups) != 2 || groups[0].base != "alpha-1" || groups[1].base != "beta-2" {
		t.Fatalf("distinct families must not collapse: %#v", groups)
	}
}

func TestGroupVariantsDropCursorVendorPrefix(t *testing.T) {
	rows := []*pb.ModelDetails{
		{ModelId: "cursor-grok-4.6-low", DisplayName: "Grok 4.6 Low"},
		{ModelId: "cursor-grok-4.6-high", DisplayName: "Grok 4.6"},
		{ModelId: "grok-4.7-medium", DisplayName: "Grok 4.7"},
		{ModelId: "cursor-grok-4.7-low", DisplayName: "Prefixed clash"},
		{ModelId: "cursor-native-1", DisplayName: "Cursor own model"},
		{ModelId: "cursor-grok-", DisplayName: "Bare family"},
	}
	byBase := map[string]*variantGroup{}
	for _, g := range groupVariants(rows) {
		byBase[g.base] = g
	}
	if g := byBase["grok-4.6"]; g == nil || g.ids["low"] != "cursor-grok-4.6-low" || g.ids["high"] != "cursor-grok-4.6-high" || g.renamedFrom != "cursor-grok-4.6" {
		t.Fatalf("vendor prefix not dropped or upstream IDs lost: %#v", byBase)
	}
	if byBase["cursor-grok-4.6"] != nil || byBase["grok-4.7"] == nil {
		t.Fatalf("unexpected families: %#v", byBase)
	}
	// A plain family that already owns the public name keeps it; Cursor's
	// own cursor-* models and a bare vendor prefix are never renamed.
	if byBase["cursor-grok-4.7"] == nil || byBase["cursor-native-1"] == nil || byBase["cursor-grok-"] == nil {
		t.Fatalf("collision, own model or bare prefix renamed: %#v", byBase)
	}
}

func TestResolveExecutionModelExplainsVendorRename(t *testing.T) {
	table := &variantTable{byBase: map[string]*variantGroup{
		"grok-4.6": {base: "grok-4.6", renamedFrom: "cursor-grok-4.6", ids: map[string]string{"low": "cursor-grok-4.6-low", "high": "cursor-grok-4.6-high"}},
		"grok-4.7": {base: "grok-4.7", ids: map[string]string{"high": "grok-4.7-high"}},
	}}
	e, req := warmedExecutor(t, table)
	for model, want := range map[string]string{"grok-4.6": "cursor-grok-4.6-high", "grok-4.6(low)": "cursor-grok-4.6-low"} {
		req.Model = model
		if got, _, err := e.resolveExecutionModel(context.Background(), req); err != nil || got != want {
			t.Fatalf("%s: %q %v, want %q", model, got, err, want)
		}
	}
	for _, old := range []string{"cursor-grok-4.6", "cursor-grok-4.6-high"} {
		req.Model = old
		if _, _, err := e.resolveExecutionModel(context.Background(), req); err == nil || !strings.Contains(err.Error(), "use grok-4.6") {
			t.Fatalf("%s should explain the rename: %v", old, err)
		}
	}
	req.Model = "cursor-grok-4.7"
	if _, _, err := e.resolveExecutionModel(context.Background(), req); err == nil || strings.Contains(err.Error(), "renamed") {
		t.Fatalf("never-prefixed family must not claim a rename: %v", err)
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

func TestStreamResolvesVariantBeforeBuild(t *testing.T) {
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initial := readClient(t, r.Body).GetRunRequest()
		if initial == nil || initial.RequestedModel.ModelId != "base-high" {
			t.Errorf("stream upstreamed unresolved model: %#v", initial.GetRequestedModel())
		}
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, endTurn())
	}))
	s.EnableHTTP2 = true
	s.StartTLS()
	e := NewExecutor(Config{Enabled: true, APIBaseURL: s.URL, RequestTimeout: 5})
	e.client = s.Client()
	e.variants = map[string]variantCacheEntry{
		variantCacheKey("fixture-secret"): {table: &variantTable{byBase: map[string]*variantGroup{
			"base": {base: "base", display: "Base", ids: map[string]string{"low": "base-low", "high": "base-high"}},
		}}, expires: time.Now().Add(variantCacheTTL)},
	}
	t.Cleanup(func() { e.Close(); s.Close() })
	req := testRequest()
	req.Model = "base(high)"
	stream, err := e.ExecuteStream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
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
	got, _, err := e.resolveExecutionModel(context.Background(), req)
	if err != nil || got != "claude-opus-4-8-medium" {
		t.Fatalf("default: %q %v", got, err)
	}
	req.Model = "claude-opus-4-8(low)"
	if got, _, err := e.resolveExecutionModel(context.Background(), req); err != nil || got != "claude-opus-4-8-low" {
		t.Fatalf("suffix: %q %v", got, err)
	}
	req.Model = "claude-opus-4-8-high"
	if _, _, err := e.resolveExecutionModel(context.Background(), req); err == nil {
		t.Fatal("legacy suffixed ID should explain the migration")
	}
	req.Model = "no-such-model"
	if _, _, err := e.resolveExecutionModel(context.Background(), req); err == nil {
		t.Fatal("unknown model accepted")
	}
}
