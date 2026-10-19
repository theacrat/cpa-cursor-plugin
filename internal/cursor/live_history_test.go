package cursor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func liveHistoryFixture() ([]chatMessage, []livePending) {
	pending := []livePending{
		{call: toolCall{ID: "pending-first", Type: "function", Function: functionCall{Name: "skill", Arguments: `{"id":"using-agent-skills"}`}}},
		{call: toolCall{ID: "pending-second", Type: "function", Function: functionCall{Name: "lookup", Arguments: `{"query":"current"}`}}},
	}
	expected := []chatMessage{
		{Role: "system", Content: json.RawMessage(`"instructions"`)},
		{Role: "user", Content: json.RawMessage(`"earlier task"`)},
		{Role: "assistant", Content: json.RawMessage(`""`), ToolCalls: []toolCall{{ID: "completed", Type: "function", Function: functionCall{Name: "read", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "completed", Content: json.RawMessage(`"old result"`)},
		{Role: "assistant", Content: json.RawMessage(`"finished"`)},
		{Role: "user", Content: json.RawMessage(`"current task"`)},
		{Role: "assistant", Content: json.RawMessage(`"checking"`), Reasoning: "current reasoning", ToolCalls: []toolCall{pending[0].call, pending[1].call}},
	}
	return expected, pending
}

func liveHistoryPayload(t *testing.T, messages []chatMessage) []byte {
	t.Helper()
	raw, err := json.Marshal(struct {
		Messages []chatMessage `json:"messages"`
	}{Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLiveResultMessagesAcceptOwnedOmittedLastCallsAndOrderCompleteResults(t *testing.T) {
	expected, pending := liveHistoryFixture()
	content := strings.Repeat("complete skill content\n", 1000)
	large, _ := json.Marshal(content)
	first := chatMessage{Role: "tool", ToolCallID: pending[0].call.ID, Content: large, Reasoning: "preserved result field"}
	second := chatMessage{Role: "tool", ToolCallID: pending[1].call.ID, Content: json.RawMessage(`"actual tool failure"`), IsError: true}
	for _, omit := range []bool{false, true} {
		messages := append([]chatMessage(nil), expected...)
		messages[2].Content = json.RawMessage(`null`)
		if omit {
			messages[len(messages)-1].ToolCalls = nil
		}
		messages = append(messages, second, first)
		results, err := liveResultMessages(expected, pending, liveHistoryPayload(t, messages))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(results, []chatMessage{first, second}) {
			t.Fatal("result ordering, large content, error flag or fields were changed")
		}
		if len(expected[len(expected)-1].ToolCalls) != 2 || pending[0].call.ID != "pending-first" {
			t.Fatal("matching modified stored history or pending ownership")
		}
	}
}

func TestLiveResultMessagesCanonicaliseEmptyFinalAssistantContent(t *testing.T) {
	expected, pending := liveHistoryFixture()
	expected[len(expected)-1].Content = json.RawMessage(`""`)
	for _, representation := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`""`), json.RawMessage(`[{"type":"text","text":""}]`)} {
		messages := append([]chatMessage(nil), expected...)
		messages[len(messages)-1].Content = representation
		messages[len(messages)-1].ToolCalls = nil
		for _, item := range pending {
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: item.call.ID, Content: json.RawMessage(`"result"`)})
		}
		if _, err := liveResultMessages(expected, pending, liveHistoryPayload(t, messages)); err != nil {
			t.Fatalf("canonical empty assistant content was rejected: %v", err)
		}
	}
}

func TestLiveResultMessagesRejectIncompleteForeignDuplicateAndSteeredBatches(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func([]chatMessage) []chatMessage
	}{
		{"missing result", func(messages []chatMessage) []chatMessage { return messages[:len(messages)-1] }},
		{"foreign result", func(messages []chatMessage) []chatMessage {
			messages[len(messages)-1].ToolCallID = "private-sentinel"
			return messages
		}},
		{"duplicate result", func(messages []chatMessage) []chatMessage {
			messages[len(messages)-1].ToolCallID = messages[len(messages)-2].ToolCallID
			return messages
		}},
		{"extra repeated result", func(messages []chatMessage) []chatMessage { return append(messages, messages[len(messages)-1]) }},
		{"new user steer", func(messages []chatMessage) []chatMessage {
			return append(messages, chatMessage{Role: "user", Content: json.RawMessage(`"private-sentinel"`)})
		}},
		{"result replaced by user", func(messages []chatMessage) []chatMessage { messages[len(messages)-1].Role = "user"; return messages }},
		{"changed earlier prefix", func(messages []chatMessage) []chatMessage {
			messages[1].Content = json.RawMessage(`"private-sentinel"`)
			return messages
		}},
		{"changed earlier result", func(messages []chatMessage) []chatMessage {
			messages[3].Content = json.RawMessage(`"private-sentinel"`)
			return messages
		}},
		{"removed earlier calls", func(messages []chatMessage) []chatMessage { messages[2].ToolCalls = nil; return messages }},
		{"changed final reasoning", func(messages []chatMessage) []chatMessage {
			messages[6].Reasoning = "private-sentinel"
			return messages
		}},
		{"changed final text", func(messages []chatMessage) []chatMessage {
			messages[6].Content = json.RawMessage(`"private-sentinel"`)
			return messages
		}},
		{"partially omitted calls", func(messages []chatMessage) []chatMessage {
			messages[6].ToolCalls = messages[6].ToolCalls[:1]
			return messages
		}},
		{"conflicting final call", func(messages []chatMessage) []chatMessage {
			messages[6].ToolCalls = append([]toolCall(nil), messages[6].ToolCalls...)
			messages[6].ToolCalls[0].Function.Name = "private-sentinel"
			return messages
		}},
		{"missing entire final assistant", func(messages []chatMessage) []chatMessage { return append(messages[:6], messages[7:]...) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, pending := liveHistoryFixture()
			messages := append([]chatMessage(nil), expected...)
			for _, item := range pending {
				messages = append(messages, chatMessage{Role: "tool", ToolCallID: item.call.ID, Content: json.RawMessage(`"result"`)})
			}
			results, err := liveResultMessages(expected, pending, liveHistoryPayload(t, test.change(messages)))
			if results != nil || err == nil || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("invalid result association did not fail safely: %v", err)
			}
		})
	}
}

func TestLiveResultMessagesRequirePendingProofMatchingExpectedCalls(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func([]livePending) []livePending
	}{
		{"no pending", func([]livePending) []livePending { return nil }},
		{"missing pending", func(pending []livePending) []livePending { return pending[:1] }},
		{"duplicate pending", func(pending []livePending) []livePending { pending[1] = pending[0]; return pending }},
		{"blank pending ID", func(pending []livePending) []livePending { pending[0].call.ID = ""; return pending }},
		{"different pending original function", func(pending []livePending) []livePending {
			pending[0].call.Function.Name = "private-sentinel"
			return pending
		}},
		{"different pending arguments", func(pending []livePending) []livePending {
			pending[0].call.Function.Arguments = `{"private-sentinel":true}`
			return pending
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, pending := liveHistoryFixture()
			messages := append([]chatMessage(nil), expected...)
			messages[len(messages)-1].ToolCalls = nil
			for _, item := range pending {
				messages = append(messages, chatMessage{Role: "tool", ToolCallID: item.call.ID, Content: json.RawMessage(`"result"`)})
			}
			results, err := liveResultMessages(expected, test.change(pending), liveHistoryPayload(t, messages))
			if results != nil || err == nil || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatalf("unproven pending association did not fail safely: %v", err)
			}
		})
	}
}

func TestLiveResultMessagesRejectMalformedPayloadAndMissingExpectedAssistant(t *testing.T) {
	expected, pending := liveHistoryFixture()
	for _, raw := range [][]byte{nil, []byte(`{"messages":`), liveHistoryPayload(t, nil)} {
		if results, err := liveResultMessages(expected, pending, raw); results != nil || err == nil {
			t.Fatal("invalid transcript was accepted")
		}
	}
	if results, err := liveResultMessages(expected[:6], pending, liveHistoryPayload(t, expected)); results != nil || err == nil {
		t.Fatal("missing expected assistant mapping was accepted")
	}
	if results, err := liveResultMessages(nil, pending, liveHistoryPayload(t, expected)); results != nil || err == nil {
		t.Fatal("absent expected history was accepted")
	}
}

func TestLiveResultMessagesMatchStoredCallsByIDAndReturnPendingOrder(t *testing.T) {
	expected, pending := liveHistoryFixture()
	last := &expected[len(expected)-1]
	last.ToolCalls[0], last.ToolCalls[1] = last.ToolCalls[1], last.ToolCalls[0]
	messages := append([]chatMessage(nil), expected...)
	messages[len(messages)-1].ToolCalls = nil
	for _, item := range pending {
		messages = append(messages, chatMessage{Role: "tool", ToolCallID: item.call.ID, Content: json.RawMessage(`"result"`)})
	}
	results, err := liveResultMessages(expected, pending, liveHistoryPayload(t, messages))
	if err != nil || len(results) != 2 || results[0].ToolCallID != pending[0].call.ID || results[1].ToolCallID != pending[1].call.ID {
		t.Fatalf("stored-call ordering changed ownership: %v", err)
	}
}
