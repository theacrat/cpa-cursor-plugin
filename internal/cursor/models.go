package cursor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
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
	if base == "" {
		base = "https://api2.cursor.sh"
	}
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
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, errors.New("Cursor model discovery failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return pluginapi.ModelResponse{Provider: "cursor"}, fmt.Errorf("Cursor model discovery returned HTTP %d", response.StatusCode)
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
		models = append(models, pluginapi.ModelInfo{ID: m.ModelId, Object: "model", Created: time.Now().Unix(), OwnedBy: "cursor", Type: "chat", DisplayName: first(m.DisplayName, m.ModelId), Name: m.ModelId})
	}
	return pluginapi.ModelResponse{Provider: "cursor", Models: models}, nil
}
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
