package cursor

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func (e *Executor) ModelsForAuth(ctx context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	if !e.cfg.Enabled {
		return pluginapi.ModelResponse{Provider: "cursor"}, errors.New("Cursor provider is disabled")
	}
	if !req.Host.ForceModelPrefix {
		e.clearModelSnapshot(req)
		if e.Log != nil {
			e.Log("Cursor models withheld: set top-level force-model-prefix: true to prevent cross-provider routing")
		}
		return pluginapi.ModelResponse{Provider: "cursor"}, nil
	}
	if req.HTTPClient == nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, errors.New("Cursor model discovery requires the host HTTP client")
	}
	ctx, release, err := e.lease(ctx)
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.cfg.RequestTimeout)*time.Second)
	defer cancel()
	base := e.cfg.APIBaseURL
	target, err := endpoint(base, "/aiserver.v1.AiService/GetUsableModels")
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, err
	}
	h, err := e.headers(req.StorageJSON, req.Metadata, false, uuid())
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, err
	}
	body, _ := proto.Marshal(&pb.GetUsableModelsRequest{})
	response, err := req.HTTPClient.Do(ctx, pluginapi.HTTPRequest{Method: http.MethodPost, URL: target, Headers: h, Body: body})
	if ctx.Err() != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, ctx.Err()
	}
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, errors.New("Cursor model discovery failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return pluginapi.ModelResponse{Provider: "cursor"}, &HTTPStatusError{StatusCode: response.StatusCode}
	}
	if len(response.Body) > maxFrame {
		return pluginapi.ModelResponse{Provider: "cursor"}, errors.New("Cursor model discovery exceeds limit")
	}
	decoded := &pb.GetUsableModelsResponse{}
	if err := proto.Unmarshal(response.Body, decoded); err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, errors.New("Cursor returned invalid model discovery protobuf")
	}
	groups := groupVariants(decoded.Models)
	models := make([]pluginapi.ModelInfo, 0, len(groups))
	tables := map[string]*variantGroup{}
	for _, group := range groups {
		if _, dup := tables[group.base]; dup {
			continue
		}
		tables[group.base] = group
		models = append(models, pluginapi.ModelInfo{ID: group.base, Object: "model", OwnedBy: "cursor", Type: "chat", DisplayName: group.display, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"text"}})
	}
	e.variantMu.Lock()
	if token, err := executorToken(req.StorageJSON, req.Metadata); err == nil {
		e.cacheVariantsLocked(variantCacheKey(token), &variantTable{byBase: tables})
	}
	e.variantMu.Unlock()
	availableURL, err := endpoint(base, "/aiserver.v1.AiService/AvailableModels")
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, err
	}
	request := protowire.AppendVarint(protowire.AppendTag(nil, 5, protowire.VarintType), 1)
	request = protowire.AppendVarint(protowire.AppendTag(request, 7, protowire.VarintType), 1)
	for _, m := range decoded.Models {
		request = protowire.AppendString(protowire.AppendTag(request, 4, protowire.BytesType), m.ModelId)
	}
	optionalCtx, optionalCancel := context.WithTimeout(ctx, 2*time.Second)
	optional, optionalErr := req.HTTPClient.Do(optionalCtx, pluginapi.HTTPRequest{Method: http.MethodPost, URL: availableURL, Headers: h, Body: request})
	optionalCancel()
	if optionalErr == nil && optional.StatusCode >= 200 && optional.StatusCode < 300 && len(optional.Body) <= maxFrame {
		if enrichment, err := availableModelInfo(optional.Body); err == nil {
			for i := range models {
				applyAvailableMetadata(&models[i], tables[models[i].ID], enrichment)
			}
		}
	}
	for i := range models {
		if levels := advertisedLevels(tables[models[i].ID]); len(levels) > 0 || tables[models[i].ID].hasThinking {
			group := tables[models[i].ID]
			models[i].Thinking = &pluginapi.ThinkingSupport{Levels: levels, ZeroAllowed: group.hasBare && group.ids["thinking"] != "" && !group.defaultThinking}
			if len(levels) > 0 {
				models[i].SupportedParameters = append(models[i].SupportedParameters, "reasoning_effort")
			}
		}
	}
	e.storeModelSnapshot(req, models)
	disambiguateDisplayNames(models)
	return pluginapi.ModelResponse{Provider: "cursor", Models: models}, nil
}

func disambiguateDisplayNames(models []pluginapi.ModelInfo) {
	counts := map[string]int{}
	for _, m := range models {
		counts[m.DisplayName]++
	}
	for index := range models {
		if counts[models[index].DisplayName] > 1 && strings.HasSuffix(models[index].ID, "-fast") {
			models[index].DisplayName += " (Fast)"
		}
	}
	clear(counts)
	for _, model := range models {
		counts[model.DisplayName]++
	}
	seen := map[string]int{}
	for i := range models {
		if counts[models[i].DisplayName] < 2 {
			continue
		}
		suffix := effortSuffix(models[i].ID)
		if suffix == "" {
			suffix = models[i].ID
		}
		name := models[i].DisplayName + " [" + suffix + "]"
		if seen[name] > 0 {
			name = models[i].DisplayName + " [" + models[i].ID + "]"
		}
		seen[name]++
		models[i].DisplayName = name
	}
}

func effortSuffix(id string) string {
	segment := id
	if i := strings.LastIndex(segment, "/"); i >= 0 {
		segment = segment[i+1:]
	}
	if i := strings.LastIndex(segment, "-"); i >= 0 {
		segment = segment[i+1:]
	}
	switch strings.ToLower(segment) {
	case "low", "medium", "high", "max", "minimal", "thinking":
		return strings.ToLower(segment)
	default:
		return ""
	}
}

func eachWireField(raw []byte, visit func(protowire.Number, protowire.Type, []byte) error) error {
	for len(raw) > 0 {
		number, kind, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return errors.New("invalid AvailableModels tag")
		}
		raw = raw[n:]
		length := protowire.ConsumeFieldValue(number, kind, raw)
		if length < 0 {
			return errors.New("invalid AvailableModels field")
		}
		value := raw[:length]
		if kind == protowire.BytesType {
			value, n = protowire.ConsumeBytes(value)
			if n < 0 {
				return errors.New("invalid AvailableModels bytes")
			}
		}
		if err := visit(number, kind, value); err != nil {
			return err
		}
		raw = raw[length:]
	}
	return nil
}
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
