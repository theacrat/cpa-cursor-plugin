package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const maxModelSnapshots = 128
const maxSnapshotModels = 2048

type modelSnapshot struct {
	models  map[string]pluginapi.ModelInfo
	updated time.Time
}

func modelSnapshotKey(req pluginapi.AuthModelRequest) string {
	if req.AuthID != "" {
		return req.AuthID
	}
	token, _ := executorToken(req.StorageJSON, req.Metadata)
	return variantCacheKey(token)
}

func (e *Executor) clearModelSnapshot(req pluginapi.AuthModelRequest) {
	e.modelMu.Lock()
	delete(e.modelSnapshots, modelSnapshotKey(req))
	e.modelMu.Unlock()
}

func (e *Executor) storeModelSnapshot(req pluginapi.AuthModelRequest, models []pluginapi.ModelInfo) {
	storage, err := credentialData(req.StorageJSON, req.Metadata)
	if err != nil || len(models) > maxSnapshotModels {
		return
	}
	prefix := first(stringField(storage, "prefix"), e.cfg.ModelPrefix)
	if !validModelPrefix(prefix) {
		return
	}
	snapshot := modelSnapshot{models: make(map[string]pluginapi.ModelInfo, len(models)), updated: time.Now()}
	for _, model := range models {
		snapshot.models[prefix+"/"+model.ID] = model
	}
	e.modelMu.Lock()
	defer e.modelMu.Unlock()
	if e.modelSnapshots == nil {
		e.modelSnapshots = make(map[string]modelSnapshot)
	}
	key := modelSnapshotKey(req)
	if _, exists := e.modelSnapshots[key]; !exists && len(e.modelSnapshots) >= maxModelSnapshots {
		var oldest string
		for candidate, previous := range e.modelSnapshots {
			if oldest == "" || previous.updated.Before(e.modelSnapshots[oldest].updated) {
				oldest = candidate
			}
		}
		delete(e.modelSnapshots, oldest)
	}
	e.modelSnapshots[key] = snapshot
}

func (e *Executor) publishedMetadata(id string) (pluginapi.ModelInfo, bool) {
	e.modelMu.Lock()
	defer e.modelMu.Unlock()
	var result pluginapi.ModelInfo
	found := false
	for _, snapshot := range e.modelSnapshots {
		model, ok := snapshot.models[id]
		if !ok {
			continue
		}
		if !found {
			result, found = model, true
			continue
		}
		result.ContextLength = minKnownLimit(result.ContextLength, model.ContextLength)
		result.InputTokenLimit = minKnownLimit(result.InputTokenLimit, model.InputTokenLimit)
		result.OutputTokenLimit = minKnownLimit(result.OutputTokenLimit, model.OutputTokenLimit)
		result.MaxCompletionTokens = minKnownLimit(result.MaxCompletionTokens, model.MaxCompletionTokens)
		result.SupportedInputModalities = sharedModelValues(result.SupportedInputModalities, model.SupportedInputModalities)
		result.SupportedParameters = sharedModelValues(result.SupportedParameters, model.SupportedParameters)
		if result.Thinking == nil || model.Thinking == nil {
			result.Thinking = nil
		} else {
			result.Thinking = &pluginapi.ThinkingSupport{Levels: sharedModelValues(result.Thinking.Levels, model.Thinking.Levels), ZeroAllowed: result.Thinking.ZeroAllowed && model.Thinking.ZeroAllowed}
		}
	}
	return result, found
}

func minKnownLimit(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	return min(a, b)
}

func sharedModelValues(a, b []string) []string {
	var values []string
	for _, value := range a {
		if slices.Contains(b, value) {
			values = append(values, value)
		}
	}
	return values
}

func (e *Executor) InterceptResponse(ctx context.Context, req pluginapi.ResponseInterceptRequest) (pluginapi.ResponseInterceptResponse, error) {
	if !e.cfg.Enabled || req.Stream || req.Model != "" || req.RequestedModel != "" || len(req.OriginalRequest) != 0 || len(req.RequestBody) != 0 || len(req.Metadata) != 0 || req.StatusCode != 0 && req.StatusCode != 200 || len(req.Body) > maxFrame || ctx.Err() != nil {
		return pluginapi.ResponseInterceptResponse{}, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(req.Body, &root) != nil {
		return pluginapi.ResponseInterceptResponse{}, nil
	}
	changed := false
	for _, schema := range []struct{ list, id string }{{"data", "id"}, {"models", "slug"}} {
		var rows []json.RawMessage
		if json.Unmarshal(root[schema.list], &rows) != nil {
			continue
		}
		entries := e.modelListLabels(rows, schema.id)
		for index, entry := range entries {
			if ctx.Err() != nil {
				return pluginapi.ResponseInterceptResponse{}, nil
			}
			if !entry.managed {
				continue
			}
			metadataChanged := patchModelMetadata(entry.row, entry.model, schema.list == "models")
			if !metadataChanged && !entry.changed {
				continue
			}
			patched, err := json.Marshal(entry.row)
			if err == nil && !bytes.Equal(patched, rows[index]) {
				rows[index] = patched
				changed = true
			}
		}
		root[schema.list], _ = json.Marshal(rows)
	}
	if !changed {
		return pluginapi.ResponseInterceptResponse{}, nil
	}
	body, err := json.Marshal(root)
	return pluginapi.ResponseInterceptResponse{Body: body}, err
}

func patchModelMetadata(row map[string]json.RawMessage, model pluginapi.ModelInfo, codex bool) bool {
	changed := false
	put := func(key string, value any) {
		row[key], _ = json.Marshal(value)
		changed = true
	}
	putMissing := func(key string, value any) {
		if _, present := row[key]; !present {
			put(key, value)
		}
	}
	contextKey, outputKey := "context_length", "max_completion_tokens"
	if codex {
		contextKey, outputKey = "context_window", "max_tokens"
	}
	if model.ContextLength > 0 {
		putMissing(contextKey, model.ContextLength)
	}
	if model.OutputTokenLimit > 0 {
		putMissing(outputKey, model.OutputTokenLimit)
	}
	putMissing("modalities", map[string][]string{"input": model.SupportedInputModalities, "output": model.SupportedOutputModalities})
	if len(model.SupportedParameters) > 0 {
		putMissing("supported_parameters", model.SupportedParameters)
	}
	if slices.Contains(model.SupportedParameters, "tools") {
		putMissing("tool_call", true)
	}
	for _, key := range []string{"thinking", "reasoning", "reasoning_options", "supported_reasoning_levels"} {
		if _, present := row[key]; present {
			return changed
		}
	}
	thinking := map[string]any{"levels": []string{}}
	if model.Thinking != nil {
		thinking["levels"] = append([]string{}, model.Thinking.Levels...)
		thinking["zero_allowed"] = model.Thinking.ZeroAllowed
		put("reasoning", true)
	}
	put("thinking", thinking)
	levels := []map[string]string{}
	options := []map[string]any{}
	if model.Thinking != nil {
		for _, level := range model.Thinking.Levels {
			levels = append(levels, map[string]string{"effort": level})
		}
		if len(model.Thinking.Levels) > 0 {
			options = append(options, map[string]any{"type": "effort", "values": model.Thinking.Levels})
		}
		if model.Thinking.ZeroAllowed {
			options = append(options, map[string]any{"type": "toggle"})
		}
	}
	put("supported_reasoning_levels", levels)
	put("reasoning_options", options)
	return changed
}
