package cursor

import (
	"bytes"
	"encoding/json"
	"errors"
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
