package cursor

import (
	"context"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestLiveToolLoopRejectsBatchThatCannotFitAContinuation(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	defer e.Close()
	req := liveRequest(nil)
	built, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &liveSession{ctx: ctx, cancel: cancel, built: built, replayBytes: len(req.Payload)}
	session.retainedBytes.Store(int64(built.blobs.size))
	e.live = map[string]*liveSession{"owner": session}
	value, _ := proto.Marshal(structpb.NewStringValue(strings.Repeat("x", 8<<20)))
	var result runResult
	for index := uint32(0); index < 2; index++ {
		exec := &pb.ExecServerMessage{Id: index + 7, ExecId: "upstream", Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{ToolName: "lookup", ToolCallId: string(rune('a' + index)), Args: map[string][]byte{"large": value}}}}
		err := e.handleLiveExec(session, built, exec, &result)
		if index == 0 && err != nil {
			t.Fatal(err)
		}
		if index == 1 && (err == nil || !strings.Contains(err.Error(), "replay limit")) {
			t.Fatalf("impossible replay batch accepted: %v", err)
		}
	}
	if len(session.pending) != 1 || len(result.calls) != 1 {
		t.Fatal("oversized pending execution was published")
	}
	if session.retainedBytes.Load() <= int64(built.blobs.size) {
		t.Fatal("pending arguments were excluded from retained accounting")
	}
	clear(e.live)
}

func TestLiveToolLoopOversizedMultiFrameBatchNeverEscapesToClient(t *testing.T) {
	value, _ := proto.Marshal(structpb.NewStringValue(strings.Repeat("x", 8<<20)))
	scope := &liveFixtureScope{chunks: make(chan pluginapi.HTTPStreamChunk, 8), batch: 2, arguments: map[string][]byte{"large": value}}
	e := NewExecutor(DefaultConfig())
	warmFixtureVariants(e)
	defer e.Close()
	response, err := e.Execute(context.Background(), liveRequest(&liveFixtureHost{scope: scope}))
	if err == nil || !strings.Contains(err.Error(), "replay limit") || len(response.Payload) != 0 {
		t.Fatal("an impossible continuation batch escaped as executable client calls")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.parks != 0 || !scope.closed {
		t.Fatal("oversized batch retained pending upstream work")
	}
}
