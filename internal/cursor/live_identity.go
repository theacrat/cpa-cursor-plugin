package cursor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func liveOwner(req pluginapi.ExecutorRequest) string {
	if path, _ := req.Metadata["request_path"].(string); path != "/v1/chat/completions" {
		return ""
	}
	if req.AuthID == "" || req.AuthProvider != "cursor" || singleIdentityHeader(req.Headers, "X-Cpa-Request-Kind") != "primary" {
		return ""
	}
	session := singleIdentityHeader(req.Headers, "X-Opencode-Session-Id")
	caller, _ := req.Metadata["caller_scope"].(string)
	if session == "" || len(caller) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(caller); err != nil {
		return ""
	}
	raw, _ := json.Marshal([]string{"cursor-live-v1", req.AuthProvider, req.AuthID, caller, session, "primary"})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func singleIdentityHeader(headers http.Header, name string) string {
	var values []string
	for key, list := range headers {
		if strings.EqualFold(key, name) {
			values = append(values, list...)
		}
	}
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 256 || !utf8.ValidString(values[0]) {
		return ""
	}
	value := values[0]
	if strings.TrimSpace(value) != value {
		return ""
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ""
		}
	}
	return value
}

func liveCompatibility(req pluginapi.ExecutorRequest, b *builtRequest) string {
	token, err := executorToken(req.StorageJSON, req.AuthMetadata)
	if err != nil {
		return ""
	}
	var chat chatRequest
	if json.Unmarshal(req.Payload, &chat) != nil {
		return ""
	}
	var system []chatMessage
	for _, message := range chat.Messages {
		if message.Role == "system" || message.Role == "developer" {
			system = append(system, message)
		}
	}
	parameters, _ := json.Marshal(chat.ModelParameters)
	raw, _ := json.Marshal(struct {
		Model      string
		Token      string
		System     []chatMessage
		Tools      []*pbToolFingerprint
		Max        bool
		Parameters []byte
	}{Model: req.Model, Token: variantCacheKey(token), System: system, Tools: toolFingerprints(b), Max: chat.MaxMode, Parameters: parameters})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

type pbToolFingerprint struct{ Name, Original, Description, Schema string }

func toolFingerprints(b *builtRequest) []*pbToolFingerprint {
	tools := make([]*pbToolFingerprint, 0, len(b.tools))
	for _, tool := range b.tools {
		tools = append(tools, &pbToolFingerprint{Name: tool.Name, Original: b.resolve[tool.Name], Description: tool.Description, Schema: tool.GetInputSchemaJson()})
	}
	return tools
}
