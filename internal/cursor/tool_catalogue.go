package cursor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const toolCatalogueLimit = 256

type toolCatalogueEntry struct {
	key   string
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
	if session == "" || caller == "" {
		return ""
	}
	raw, _ := json.Marshal([]string{req.AuthProvider, req.AuthID, req.Model, caller, session})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
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
	if _, exists := s.entries[owner]; !exists {
		s.order = append(s.order, owner)
	}
	s.entries[owner] = toolCatalogueEntry{key: owner, tools: raw}
	for len(s.order) > toolCatalogueLimit {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
}

func (s *toolCatalogueStore) restore(req pluginapi.ExecutorRequest) (pluginapi.ExecutorRequest, error) {
	if !toolResultContinuation(req.Payload) || toolCatalogueOwner(req) == "" {
		return req, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(req.Payload, &fields) != nil {
		return req, errors.New("invalid Cursor chat-completions payload")
	}
	tools, exists := fields["tools"]
	if exists && string(tools) != "null" && string(tools) != "[]" {
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
