package cursor

import (
	"context"
	"encoding/json"
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
	token, err := executorToken(req.StorageJSON, req.Metadata)
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, err
	}
	base := e.cfg.APIBaseURL
	if base == "" {
		base = "https://api2.cursor.sh"
	}
	target, err := endpoint(base, "/aiserver.v1.AiService/GetUsableModels")
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, err
	}
	h := http.Header{"Authorization": []string{"Bearer " + token}, "Content-Type": []string{"application/proto"}, "X-Cursor-Client-Version": []string{e.cfg.ClientVersion}, "X-Cursor-Client-Type": []string{"cli"}}
	if team, _ := req.Metadata["selected_team_id"].(string); team != "" {
		h.Set("X-Cursor-Team-Id", team)
	}
	body, _ := proto.Marshal(&pb.GetUsableModelsRequest{})
	response, err := req.HTTPClient.Do(ctx, pluginapi.HTTPRequest{Method: http.MethodPost, URL: target, Headers: h, Body: body})
	if err != nil {
		return pluginapi.ModelResponse{Provider: "cursor"}, errors.New("Cursor model discovery failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return pluginapi.ModelResponse{Provider: "cursor"}, fmt.Errorf("Cursor model discovery returned HTTP %d", response.StatusCode)
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

// AvailableModels in official CLI responses is intentionally not decoded via
// JSON. The protobuf schema is authoritative; this helper is used by tests to
// make sure no discovery payload is accidentally logged.
func discoveryMetadata(raw []byte) map[string]any {
	var x map[string]any
	_ = json.Unmarshal(raw, &x)
	return x
}
