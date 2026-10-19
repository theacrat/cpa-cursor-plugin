package cursor

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
)

func compactionRequest(tools string) pluginapi.ExecutorRequest {
	req := testRequest()
	req.Payload = []byte(`{"messages":[{"role":"system","content":"summarise the entire conversation"},{"role":"user","content":"read a file"},{"role":"assistant","reasoning_content":"historic reasoning","tool_calls":[{"id":"historic-read","type":"function","function":{"name":"read","arguments":"{\"path\":\"/client/file\"}"}}]},{"role":"tool","tool_call_id":"historic-read","content":"old-model result"},{"role":"assistant","content":"the previous model finished"},{"role":"user","content":"write the summary now"}]` + tools + `}`)
	return req
}

func TestCompactionPreservesCompletedHistoryAcrossModels(t *testing.T) {
	for _, tools := range []string{"", `,"tools":[]`, `,"tool_choice":"none","tools":[{"type":"function","function":{"name":"read"}}]`, `,"tools":[{"type":"function","function":{"name":"lookup"}}]`} {
		b, err := buildRequest(compactionRequest(tools))
		if err != nil {
			t.Fatal(err)
		}
		run := b.message.GetRunRequest()
		if run.Action.GetUserMessageAction().GetUserMessage().Text != "write the summary now" || len(run.ConversationState.Turns) != 1 {
			t.Fatal("compaction changed the real summary request shape")
		}
		var messages [][]byte
		for _, id := range run.ConversationState.RootPromptMessagesJson {
			messages = append(messages, b.blobs.data[hex.EncodeToString(id)])
		}
		joined := bytes.Join(messages, []byte("\n"))
		for _, want := range []string{"summarise the entire conversation", "old-model result", "the previous model finished", "historic reasoning"} {
			if !bytes.Contains(joined, []byte(want)) {
				t.Fatalf("compaction erased %q", want)
			}
		}
		for _, tool := range b.tools {
			if tool.Name == "read" {
				t.Fatal("historic tool was re-exposed by summarisation")
			}
		}
	}
}

func TestCompactionExecutesFinalUserRequestAfterModelSwitch(t *testing.T) {
	_, e := tlsFixture(t, func(w http.ResponseWriter, req *http.Request) {
		run := readClient(t, req.Body).GetRunRequest()
		if run.Action.GetUserMessageAction().GetUserMessage().Text != "write the summary now" {
			t.Error("compaction did not issue the summary user prompt")
		}
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		writeServer(t, w, update(&pb.InteractionUpdate{Message: &pb.InteractionUpdate_TextDelta{TextDelta: &pb.TextDeltaUpdate{Text: "summary of previous model history"}}}))
		writeServer(t, w, endTurn())
	})
	response, err := e.Execute(context.Background(), compactionRequest(""))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response.Payload, []byte("summary of previous model history")) {
		t.Fatal("compaction response missing")
	}
}

func TestAssistantEndedHistoryRemainsRejected(t *testing.T) {
	for _, tools := range []string{"", `,"tools":[{"type":"function","function":{"name":"read"}}]`} {
		req := testRequest()
		req.Payload = []byte(`{"messages":[{"role":"user","content":"task"},{"role":"assistant","content":"unfinished?"}]` + tools + `}`)
		if _, err := buildRequest(req); err == nil {
			t.Fatal("assistant prefill was silently reinterpreted as compaction")
		}
	}
}

func TestCompactionPreservesUpstreamRateAndPermissionStatuses(t *testing.T) {
	for _, status := range []int{403, 429} {
		e := NewExecutor(DefaultConfig())
		warmFixtureVariants(e)
		req := compactionRequest("")
		req.HTTPClient = fixtureHost{stream: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
			return pluginapi.HTTPStreamResponse{StatusCode: status}, nil
		}}
		_, err := e.Execute(context.Background(), req)
		e.Close()
		var upstream *HTTPStatusError
		if !errors.As(err, &upstream) || upstream.StatusCode != status {
			t.Fatalf("compaction hid status %d: %v", status, err)
		}
	}
}
