package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
)

// Canonical thinking levels served by Cursor effort variants.
const (
	levelMinimal = "minimal"
	levelLow     = "low"
	levelMedium  = "medium"
	levelHigh    = "high"
	levelXHigh   = "xhigh"
	levelMax     = "max"
)

// familyTokens are trailing ID segments folded into one model family.
// Effort tokens double as switcher kinds; fast/none only affect ranking.
var familyTokens = map[string]bool{
	"thinking": true, levelMinimal: true, levelLow: true, levelMedium: true,
	levelHigh: true, levelXHigh: true, levelMax: true, "fast": true, "none": true,
}

// effortTokens are family tokens that select reasoning effort.
var effortTokens = map[string]bool{
	"thinking": true, levelMinimal: true, levelLow: true, levelMedium: true,
	levelHigh: true, levelXHigh: true, levelMax: true,
}

const defaultKind = "default"

// variantGroup is one model family and its known upstream variant IDs.
type variantGroup struct {
	base        string
	display     string
	ids         map[string]string
	rank        map[string]int
	hasBare     bool
	hasThinking bool
}

// variantTable maps family base IDs to their upstream variants.
type variantTable struct {
	byBase map[string]*variantGroup
}

type variantCacheEntry struct {
	table   *variantTable
	expires time.Time
}

type variantFetch struct {
	done  chan struct{}
	table *variantTable
	took  time.Duration
	err   error
}

// analyseVariant folds a discovered model ID into its family base, effort
// kind and selection rank. Trailing family tokens are stripped inside out;
// the rightmost effort token sets the kind and the standard (non-fast) tier
// outranks fast ones so each kind keeps a single deterministic ID.
func analyseVariant(id string) (base, kind string, rank int, bare bool) {
	rest := id
	kind = defaultKind
	fast := false
	for {
		i := strings.LastIndex(rest, "-")
		if i <= 0 || strings.HasSuffix(rest[:i], "/") {
			break
		}
		token := strings.ToLower(rest[i+1:])
		if token == "fast" {
			fast = true
			rest = rest[:i]
			rank += 10
			continue
		}
		if !familyTokens[token] {
			break
		}
		if effortTokens[token] && kind == defaultKind {
			kind = token
		}
		rest = rest[:i]
		rank += 10
	}
	if rest == "" || strings.HasSuffix(rest, "/") {
		return id, defaultKind, 0, true
	}
	if fast {
		rank++
	}
	return rest, kind, rank, rank == 0
}

// groupVariants clusters discovered rows by family base ID, so one row is
// published per model family whatever the upstream display names say.
func groupVariants(rows []*pb.ModelDetails) []*variantGroup {
	byBase := map[string]*variantGroup{}
	order := []string{}
	for _, row := range rows {
		if strings.TrimSpace(row.ModelId) == "" {
			continue
		}
		base, kind, rank, bare := analyseVariant(row.ModelId)
		group, ok := byBase[base]
		if !ok {
			group = &variantGroup{base: base, display: first(row.DisplayName, row.ModelId), ids: map[string]string{}, rank: map[string]int{}}
			byBase[base] = group
			order = append(order, base)
		}
		if _, ok := group.ids[kind]; !ok || rank < group.rank[kind] {
			group.ids[kind] = row.ModelId
			group.rank[kind] = rank
		}
		if bare {
			group.hasBare = true
		}
		if row.ThinkingDetails != nil {
			group.hasThinking = true
		}
	}
	out := make([]*variantGroup, 0, len(order))
	for _, base := range order {
		group := byBase[base]
		// Cursor vendor-prefixes some third-party families (cursor-grok-4.6)
		// but not others (grok-4.7); publish the vendor name so catalogues
		// match. Upstream variant IDs keep the prefix.
		if public, ok := vendorName(base); ok && byBase[public] == nil {
			group.base = public
		}
		out = append(out, group)
	}
	return out
}

// prefixedVendorFamilies lists third-party families Cursor publishes under a
// cursor- prefix. Cursor's own cursor-* models are never renamed.
var prefixedVendorFamilies = []string{"grok-"}

// vendorName returns the unprefixed public base for a vendor-prefixed
// third-party family, or false when base is not one.
func vendorName(base string) (string, bool) {
	public, ok := strings.CutPrefix(base, "cursor-")
	if !ok {
		return "", false
	}
	for _, family := range prefixedVendorFamilies {
		if strings.HasPrefix(public, family) && len(public) > len(family) {
			return public, true
		}
	}
	return "", false
}

// defaultVariantID selects the no-effort variant: the bare ID when present,
// otherwise medium, high, low, max, minimal, xhigh, thinking, in that order.
func defaultVariantID(group *variantGroup) string {
	for _, kind := range []string{defaultKind, levelMedium, levelHigh, levelLow, levelMax, levelMinimal, levelXHigh, "thinking"} {
		if id := group.ids[kind]; id != "" {
			return id
		}
	}
	return ""
}

// resolveVariant maps a canonical effort level onto a concrete upstream ID.
// Unknown, empty, none, auto and numeric efforts select the default; exact
// suffixes win, then the thinking variant, then the default.
func resolveVariant(group *variantGroup, effort string) (string, error) {
	if group == nil {
		return "", errors.New("unknown cursor model")
	}
	def := defaultVariantID(group)
	if def == "" {
		return "", errors.New("cursor model has no usable variant")
	}
	pick := func(kinds ...string) string {
		for _, kind := range kinds {
			if id := group.ids[kind]; id != "" {
				return id
			}
		}
		return def
	}
	switch effort {
	case "":
		return def, nil
	case levelMinimal:
		return pick(levelMinimal), nil
	case levelLow:
		return pick(levelLow, "thinking"), nil
	case levelMedium:
		return pick(levelMedium, "thinking"), nil
	case levelHigh:
		return pick(levelHigh, "thinking"), nil
	case levelXHigh:
		return pick(levelXHigh, levelMax, "thinking"), nil
	case levelMax:
		return pick(levelMax, "thinking"), nil
	case "thinking":
		return pick("thinking"), nil
	default:
		return def, nil
	}
}

func advertisedLevels(group *variantGroup) []string {
	var levels []string
	for _, level := range []string{levelMinimal, levelLow, levelMedium, levelHigh, levelXHigh, levelMax} {
		if group.ids[level] != "" || level == levelHigh && group.ids["thinking"] != "" {
			levels = append(levels, level)
		}
	}
	return levels
}

// splitModelSuffix divides a requested model into its base and parenthesised
// thinking suffix following CPA's model(level) convention.
func splitModelSuffix(model string) (base, suffix string) {
	model = strings.TrimSpace(model)
	open := strings.LastIndex(model, "(")
	if open < 0 || !strings.HasSuffix(model, ")") {
		return model, ""
	}
	return strings.TrimSpace(model[:open]), strings.TrimSpace(model[open+1 : len(model)-1])
}

// suffixSelection normalises a parenthesised suffix to a canonical effort.
// Levels pass through; none, auto and numeric budgets explicitly select the
// default. Absent or unrecognised suffixes are not a selection at all.
func suffixSelection(suffix string) (string, bool) {
	lowered := strings.ToLower(strings.TrimSpace(suffix))
	switch lowered {
	case levelMinimal, levelLow, levelMedium, levelHigh, levelXHigh, levelMax, "thinking":
		return lowered, true
	case "none", "auto":
		return "", true
	default:
		if lowered != "" {
			if _, err := strconv.Atoi(lowered); err == nil {
				return "", true
			}
		}
		return "", false
	}
}

// payloadSelection reads reasoning_effort from the translated chat payload.
func payloadSelection(payload []byte) (string, bool) {
	var body struct {
		ReasoningEffort any `json:"reasoning_effort"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return "", false
	}
	effort, _ := body.ReasoningEffort.(string)
	return normaliseSelection(effort)
}

// normaliseSelection maps client effort spellings onto canonical levels and
// explicit defaults. Unknown values are not a selection.
func normaliseSelection(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case levelMinimal, levelLow, levelMedium, levelHigh, levelXHigh, levelMax, "thinking":
		return strings.ToLower(strings.TrimSpace(value)), true
	case "none", "auto", "off", "disable", "disabled":
		return "", true
	default:
		return "", false
	}
}

// originalSelection reads the native thinking field of the client's original
// request by source format.
func originalSelection(sourceFormat string, original []byte) (string, bool) {
	if len(original) == 0 {
		return "", false
	}
	format := strings.ToLower(sourceFormat)
	var body map[string]any
	if json.Unmarshal(original, &body) != nil {
		return "", false
	}
	strField := func(m map[string]any, keys ...string) string {
		for _, key := range keys {
			if value, _ := m[key].(string); value != "" {
				return value
			}
		}
		return ""
	}
	objField := func(m map[string]any, key string) map[string]any {
		obj, _ := m[key].(map[string]any)
		return obj
	}
	switch {
	case strings.Contains(format, "anthropic") || strings.Contains(format, "claude") || objField(body, "thinking") != nil || objField(body, "output_config") != nil:
		if effort, ok := normaliseSelection(strField(objField(body, "output_config"), "effort")); ok {
			return effort, true
		}
		if effort, ok := normaliseSelection(strField(body, "effort")); ok {
			return effort, true
		}
		if thinking := objField(body, "thinking"); thinking != nil {
			if typ, _ := thinking["type"].(string); strings.EqualFold(typ, "enabled") {
				return "thinking", true
			}
			return "", true
		}
		return "", false
	case strings.Contains(format, "gemini"):
		config := objField(body, "generationConfig")
		thinking := objField(config, "thinkingConfig")
		return normaliseSelection(strField(thinking, "thinkingLevel"))
	case strings.Contains(format, "codex"):
		return normaliseSelection(strField(objField(body, "reasoning"), "effort"))
	default:
		return normaliseSelection(strField(body, "reasoning_effort"))
	}
}

func translatedEnabledThinking(req pluginapi.ExecutorRequest) bool {
	var payload struct {
		Effort string `json:"reasoning_effort"`
	}
	var original struct {
		Effort   string `json:"reasoning_effort"`
		Thinking struct {
			Budget *int `json:"budget_tokens"`
		} `json:"thinking"`
	}
	if json.Unmarshal(req.Payload, &payload) != nil || json.Unmarshal(req.OriginalRequest, &original) != nil {
		return false
	}
	effort, selected := originalSelection(req.SourceFormat, req.OriginalRequest)
	return strings.EqualFold(strings.TrimSpace(payload.Effort), "auto") && original.Effort == "" && selected && effort == "thinking" && original.Thinking.Budget == nil
}

func requestEffort(req pluginapi.ExecutorRequest) string {
	if _, suffix := splitModelSuffix(req.Model); suffix != "" {
		if effort, ok := suffixSelection(suffix); ok {
			return effort
		}
	}
	if translatedEnabledThinking(req) {
		return "thinking"
	}
	if effort, ok := payloadSelection(req.Payload); ok {
		return effort
	}
	if effort, ok := originalSelection(req.SourceFormat, req.OriginalRequest); ok {
		return effort
	}
	return ""
}

const variantCacheTTL = 5 * time.Minute

func variantCacheKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// variantTableFor returns the cached variant table for a credential,
// fetching it over host HTTP on a miss. A failed fetch is an error: the
// executor must not guess upstream variant IDs.
func (e *Executor) variantTableFor(ctx context.Context, req pluginapi.ExecutorRequest) (*variantTable, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	token, err := executorToken(req.StorageJSON, req.AuthMetadata)
	if err != nil {
		return nil, 0, err
	}
	key := variantCacheKey(token)
	e.variantMu.Lock()
	if entry, ok := e.variants[key]; ok && time.Now().Before(entry.expires) {
		e.variantMu.Unlock()
		return entry.table, 0, nil
	}
	if pending, ok := e.variantFetches[key]; ok {
		e.variantMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-pending.done:
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			return pending.table, pending.took, pending.err
		}
	}
	if e.variantFetches == nil {
		e.variantFetches = make(map[string]*variantFetch)
	}
	pending := &variantFetch{done: make(chan struct{})}
	e.variantFetches[key] = pending
	e.variantMu.Unlock()
	fetchStart := time.Now()
	rows, err := e.fetchVariantRows(ctx, req)
	fetchTook := time.Since(fetchStart)
	var table *variantTable
	if err == nil {
		table = &variantTable{byBase: map[string]*variantGroup{}}
		for _, group := range groupVariants(rows) {
			if _, dup := table.byBase[group.base]; !dup {
				table.byBase[group.base] = group
			}
		}
	}
	e.variantMu.Lock()
	if err == nil {
		e.cacheVariantsLocked(key, table)
	}
	pending.table, pending.took, pending.err = table, fetchTook, err
	delete(e.variantFetches, key)
	close(pending.done)
	e.variantMu.Unlock()
	return table, fetchTook, err
}

func (e *Executor) cacheVariantsLocked(key string, table *variantTable) {
	now := time.Now()
	for key, entry := range e.variants {
		if !now.Before(entry.expires) {
			delete(e.variants, key)
		}
	}
	if e.variants == nil {
		e.variants = make(map[string]variantCacheEntry)
	}
	e.variants[key] = variantCacheEntry{table: table, expires: now.Add(variantCacheTTL)}
}

// fetchVariantRows runs authenticated model discovery for the executor's own
// variant mapping. It shares the official discovery route with ModelsForAuth
// but skips catalogue enrichment, which execution does not need.
func (e *Executor) fetchVariantRows(ctx context.Context, req pluginapi.ExecutorRequest) ([]*pb.ModelDetails, error) {
	if req.HTTPClient == nil {
		return nil, errors.New("Cursor model discovery requires the host HTTP client")
	}
	cfg := e.cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	target, err := endpoint(cfg.APIBaseURL, "/aiserver.v1.AiService/GetUsableModels")
	if err != nil {
		return nil, err
	}
	headers, err := e.headers(req.StorageJSON, req.AuthMetadata, false, uuid())
	if err != nil {
		return nil, err
	}
	body, _ := proto.Marshal(&pb.GetUsableModelsRequest{})
	timeout := time.Duration(cfg.RequestTimeout) * time.Second
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := req.HTTPClient.Do(ctx, pluginapi.HTTPRequest{Method: http.MethodPost, URL: target, Headers: headers, Body: body})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, errors.New("Cursor model discovery failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &HTTPStatusError{StatusCode: response.StatusCode}
	}
	if len(response.Body) > maxFrame {
		return nil, errors.New("Cursor model discovery exceeds limit")
	}
	decoded := &pb.GetUsableModelsResponse{}
	if err := proto.Unmarshal(response.Body, decoded); err != nil {
		return nil, errors.New("Cursor returned invalid model discovery protobuf")
	}
	return decoded.Models, nil
}

// resolveExecutionModel maps the requested model onto a concrete upstream
// variant ID, honouring the thinking switcher. It also reports how long a
// cold variant-table fetch took so run timings can attribute it.
func (e *Executor) resolveExecutionModel(ctx context.Context, req pluginapi.ExecutorRequest) (string, time.Duration, error) {
	base, _ := splitModelSuffix(req.Model)
	if strings.TrimSpace(base) == "" {
		return "", 0, errors.New("Cursor model is required")
	}
	table, fetchTook, err := e.variantTableFor(ctx, req)
	if err != nil {
		return "", fetchTook, err
	}
	group := table.byBase[base]
	if group == nil {
		// CPA only routes registered IDs here, so retired names (per-effort
		// IDs, cursor-grok-*, unprefixed IDs) never reach this point.
		return "", fetchTook, errors.New("unknown cursor model")
	}
	effort := requestEffort(req)
	if effort == "thinking" && group.ids["thinking"] == "" {
		effort = ""
	}
	variant, err := resolveVariant(group, effort)
	return variant, fetchTook, err
}
