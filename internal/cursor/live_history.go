package cursor

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func liveResultMessages(expected []chatMessage, pending []livePending, payload []byte) ([]chatMessage, error) {
	invalid := errors.New("Cursor retained result history does not match pending executions")
	if len(expected) == 0 || len(pending) == 0 {
		return nil, invalid
	}
	last := expected[len(expected)-1]
	if last.Role != "assistant" || len(last.ToolCalls) != len(pending) {
		return nil, invalid
	}
	owned := make(map[string]toolCall, len(pending))
	for _, item := range pending {
		if item.call.ID == "" || owned[item.call.ID].ID != "" {
			return nil, invalid
		}
		owned[item.call.ID] = item.call
	}
	matched := make(map[string]bool, len(last.ToolCalls))
	for _, call := range last.ToolCalls {
		if call.ID == "" || matched[call.ID] || owned[call.ID] != call {
			return nil, invalid
		}
		matched[call.ID] = true
	}
	var chat chatRequest
	if json.Unmarshal(payload, &chat) != nil || len(chat.Messages) != len(expected)+len(pending) {
		return nil, invalid
	}
	for index, want := range expected {
		got := chat.Messages[index]
		if index == len(expected)-1 && len(got.ToolCalls) == 0 {
			got.ToolCalls = want.ToolCalls
		}
		left, leftErr := json.Marshal(canonicalLiveMessage(want))
		right, rightErr := json.Marshal(canonicalLiveMessage(got))
		if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
			return nil, invalid
		}
	}
	byID := make(map[string]chatMessage, len(pending))
	for _, message := range chat.Messages[len(expected):] {
		if message.Role != "tool" || owned[message.ToolCallID].ID == "" || byID[message.ToolCallID].Role != "" {
			return nil, invalid
		}
		byID[message.ToolCallID] = message
	}
	results := make([]chatMessage, 0, len(pending))
	for _, item := range pending {
		message, exists := byID[item.call.ID]
		if !exists {
			return nil, invalid
		}
		results = append(results, message)
	}
	return results, nil
}

func (e *Executor) repairLiveReplay(req pluginapi.ExecutorRequest) (pluginapi.ExecutorRequest, error) {
	host, ok := req.HTTPClient.(RetainedHTTPHost)
	if !ok || !host.SupportsRetainedHTTP() || liveOwner(req) == "" || len(req.Payload) > maxFrame {
		return req, nil
	}
	var chat chatRequest
	if json.Unmarshal(req.Payload, &chat) != nil || len(chat.Messages) == 0 || chat.Messages[len(chat.Messages)-1].Role != "tool" {
		return req, nil
	}
	assistant := len(chat.Messages) - 1
	for assistant >= 0 && chat.Messages[assistant].Role == "tool" {
		assistant--
	}
	if assistant < 0 || chat.Messages[assistant].Role != "assistant" || len(chat.Messages[assistant].ToolCalls) != 0 {
		return req, nil
	}
	e.liveMu.Lock()
	session := e.live[liveOwner(req)]
	if session == nil || !session.mu.TryLock() {
		e.liveMu.Unlock()
		return req, nil
	}
	e.liveMu.Unlock()
	defer session.mu.Unlock()
	if !session.parked || session.closed {
		return req, nil
	}
	if _, err := liveResultMessages(session.expected, session.pending, req.Payload); err != nil {
		return req, err
	}
	var fields map[string]json.RawMessage
	var messages []map[string]json.RawMessage
	if json.Unmarshal(req.Payload, &fields) != nil || json.Unmarshal(fields["messages"], &messages) != nil {
		return req, errors.New("invalid Cursor live transcript")
	}
	messages[assistant]["tool_calls"], _ = json.Marshal(session.expected[len(session.expected)-1].ToolCalls)
	fields["messages"], _ = json.Marshal(messages)
	req.Payload, _ = json.Marshal(fields)
	return req, nil
}
