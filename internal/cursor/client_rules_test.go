package cursor

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"google.golang.org/protobuf/proto"
)

func TestClientPolicyUsesNativeGlobalRulesWithoutChangingTools(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		req := clientToolsRequest(t)
		if disabled {
			req.Payload = []byte(`{"messages":[{"role":"user","content":"summarise"}],"tools":[{"type":"function","function":{"name":"read"}}],"tool_choice":"none"}`)
		}
		b, err := buildRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		run := b.message.GetRunRequest()
		policy := clientToolPolicy(b.tools)
		root := b.blobs.data[hex.EncodeToString(run.ConversationState.RootPromptMessagesJson[1])]
		if !bytes.Contains(root, []byte(policy)) {
			t.Fatal("root prompt policy does not match native policy")
		}
		reply, err := execReply(&pb.ExecServerMessage{Id: 7, ExecId: "request-context", Message: &pb.ExecServerMessage_RequestContextArgs{RequestContextArgs: &pb.RequestContextArgs{}}}, b.tools)
		if err != nil {
			t.Fatal(err)
		}
		contexts := []*pb.RequestContext{run.Action.GetUserMessageAction().RequestContext, reply.GetExecClientMessage().GetRequestContextResult().GetSuccess().RequestContext}
		for _, context := range contexts {
			raw, err := proto.Marshal(context)
			if err != nil {
				t.Fatal(err)
			}
			decoded := new(pb.RequestContext)
			if err := proto.Unmarshal(raw, decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Rules) != 1 || decoded.Rules[0].Type.GetGlobal() == nil || decoded.Rules[0].Content != policy {
				t.Fatal("native global rules omit the executable-client policy")
			}
			if len(decoded.McpInstructions) != 1 || decoded.McpInstructions[0].Instructions != policy {
				t.Fatal("MCP policy differs from native global rules")
			}
			if len(decoded.Tools) != len(b.tools) || decoded.WebSearchEnabled == nil || *decoded.WebSearchEnabled || decoded.McpFileSystemOptions.GetEnabled() {
				t.Fatal("native rule delivery changed executable capabilities")
			}
			if disabled && (strings.Contains(decoded.Rules[0].Content, "mcp_cpa_read") || !strings.Contains(decoded.Rules[0].Content, "No client tools")) {
				t.Fatal("tool-free native rules revived disabled tools")
			}
			if !disabled && !strings.Contains(decoded.Rules[0].Content, "mcp_cpa_read") {
				t.Fatal("native rules omit registered client names")
			}
		}
	}
}
