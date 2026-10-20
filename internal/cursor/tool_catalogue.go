package cursor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const toolCatalogueLimit = 256

type toolCatalogueEntry struct {
	tools json.RawMessage
}

type toolCatalogueStore struct {
	mu      sync.Mutex
	entries map[string]toolCatalogueEntry
	order   []string
}

func toolCatalogueOwner(req pluginapi.ExecutorRequest) string {
	if req.AuthID == "" || req.AuthProvider == "" {
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
	raw, _ := json.Marshal([]string{req.AuthProvider, req.AuthID, req.Model, caller, session})
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

func (s *toolCatalogueStore) remember(req pluginapi.ExecutorRequest) {
	owner := toolCatalogueOwner(req)
	if owner == "" {
		return
	}
	var chat chatRequest
	if json.Unmarshal(req.Payload, &chat) != nil || len(chat.Tools) == 0 {
		return
	}
	raw, err := json.Marshal(chat.Tools)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]toolCatalogueEntry)
	}
	if _, exists := s.entries[owner]; exists {
		for index, key := range s.order {
			if key == owner {
				s.order = append(s.order[:index], s.order[index+1:]...)
				break
			}
		}
	}
	s.order = append(s.order, owner)
	s.entries[owner] = toolCatalogueEntry{tools: raw}
	for len(s.order) > toolCatalogueLimit {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
}

func (s *toolCatalogueStore) restore(req pluginapi.ExecutorRequest) (pluginapi.ExecutorRequest, error) {
	if toolCatalogueOwner(req) == "" {
		return req, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(req.Payload, &fields) != nil {
		return req, errors.New("invalid Cursor chat-completions payload")
	}
	_, exists := fields["tools"]
	if exists {
		return req, nil
	}
	owner := toolCatalogueOwner(req)
	s.mu.Lock()
	entry, found := s.entries[owner]
	s.mu.Unlock()
	if !found {
		return req, nil
	}
	fields["tools"] = append(json.RawMessage(nil), entry.tools...)
	restored, err := json.Marshal(fields)
	if err != nil {
		return req, errors.New("invalid Cursor chat-completions payload")
	}
	req.Payload = restored
	return req, nil
}

func newToolCatalogueStore() *toolCatalogueStore {
	return &toolCatalogueStore{}
}

func (s *toolCatalogueStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.entries)
	s.order = nil
}
