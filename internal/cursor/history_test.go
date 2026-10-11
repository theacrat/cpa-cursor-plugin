package cursor

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
)

func TestHistoryPreservesThinkingAndToolError(t *testing.T) {
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":"lookup"},{"role":"assistant","content":"checking","reasoning_content":"reasoning preserved","tool_calls":[{"id":"call1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call1","content":"lookup failed","is_error":true},{"role":"user","content":"try again"}]}`)
	b, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	state := b.message.GetRunRequest().ConversationState
	var turn pb.ConversationTurnStructure
	if err := proto.Unmarshal(b.blobs.data[hex.EncodeToString(state.Turns[0])], &turn); err != nil {
		t.Fatal(err)
	}
	thinking, failed := false, false
	for _, id := range turn.GetAgentConversationTurn().Steps {
		var step pb.ConversationStep
		if err := proto.Unmarshal(b.blobs.data[hex.EncodeToString(id)], &step); err != nil {
			t.Fatal(err)
		}
		if step.GetThinkingMessage().GetText() == "reasoning preserved" {
			thinking = true
		}
		if step.GetToolCall().GetMcpToolCall().GetResult().GetError().GetError() == "lookup failed" {
			failed = true
		}
	}
	if !thinking || !failed {
		t.Fatalf("thinking=%v failed=%v", thinking, failed)
	}
}

func TestProductionExecutorRequiresHostClient(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	_, err := e.Execute(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "host HTTP client") {
		t.Fatalf("%v", err)
	}
}
