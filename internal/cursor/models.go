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
	if req.HTTPClient == nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, errors.New("Cursor model discovery requires the host HTTP client")
	}
	ctx, release, err := e.lease(ctx)
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, err
	}
	defer release()
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
	models := make([]pluginapi.ModelInfo, 0, len(decoded.Models))
	for _, m := range decoded.Models {
		if strings.TrimSpace(m.ModelId) == "" {
			continue
		}
		models = append(models, pluginapi.ModelInfo{ID: m.ModelId, Object: "model", OwnedBy: "cursor", Type: "chat", DisplayName: first(m.DisplayName, m.ModelId), Name: m.ModelId})
	}
	// The official CLI enriches its usable catalogue with AvailableModels.
	// Discovery still succeeds when this optional endpoint is unavailable.
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
				if extra, ok := enrichment[models[i].ID]; ok {
					models[i].ContextLength = extra.ContextLength
					models[i].InputTokenLimit = extra.InputTokenLimit
					models[i].SupportedInputModalities = extra.SupportedInputModalities
					if extra.DisplayName != "" {
						models[i].DisplayName = extra.DisplayName
					}
				}
			}
		}
	}
	disambiguateDisplayNames(models)
	return pluginapi.ModelResponse{Provider: "cursor", Models: models}, nil
}

// Cursor publishes one row per reasoning-effort variant (for example
// claude-opus-4-8-low and claude-opus-4-8-high) sharing a single display
// name. Rows stay selectable by ID, but identical names are unusable in a
// model picker, so duplicated names gain their effort suffix. Unrecognised
// duplicates fall back to the full ID rather than guessing.
func disambiguateDisplayNames(models []pluginapi.ModelInfo) {
	counts := map[string]int{}
	for _, m := range models {
		counts[m.DisplayName]++
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

func availableModelInfo(raw []byte) (map[string]pluginapi.ModelInfo, error) {
	models := map[string]pluginapi.ModelInfo{}
	err := eachWireField(raw, func(number protowire.Number, kind protowire.Type, value []byte) error {
		if number != 2 || kind != protowire.BytesType {
			return nil
		}
		var model pluginapi.ModelInfo
		var images bool
		err := eachWireField(value, func(number protowire.Number, kind protowire.Type, value []byte) error {
			switch {
			case number == 1 && kind == protowire.BytesType:
				model.ID = string(value)
			case number == 17 && kind == protowire.BytesType:
				model.DisplayName = string(value)
			case number == 10 && kind == protowire.VarintType:
				v, n := protowire.ConsumeVarint(value)
				if n < 0 {
					return errors.New("invalid AvailableModels boolean")
				}
				images = v != 0
			case number == 15 && kind == protowire.VarintType:
				v, n := protowire.ConsumeVarint(value)
				if n < 0 || v > 1<<32 {
					return errors.New("invalid AvailableModels context limit")
				}
				model.ContextLength = int64(v)
				model.InputTokenLimit = int64(v)
			}
			return nil
		})
		if err != nil {
			return err
		}
		model.SupportedInputModalities = []string{"text"}
		if images {
			model.SupportedInputModalities = append(model.SupportedInputModalities, "image")
		}
		if model.ID != "" {
			models[model.ID] = model
		}
		return nil
	})
	return models, err
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
