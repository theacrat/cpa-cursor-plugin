package cursor

import (
	"strings"
	"testing"
)

func TestLiveToolLoopIdentityUsesActualSessionAndCaller(t *testing.T) {
	req := testRequest()
	req.AuthID = "account"
	req.AuthProvider = "cursor"
	req.Headers = map[string][]string{"X-Opencode-Session-Id": {"actual"}, "X-Opencode-Session": {"shared-parent"}, "X-Cpa-Request-Kind": {"primary"}}
	req.Metadata = map[string]any{"caller_scope": strings.Repeat("a", 64), "request_path": "/v1/chat/completions"}
	first := liveOwner(req)
	if first == "" {
		t.Fatal("complete ownership was rejected")
	}
	req.Headers.Set("X-Opencode-Session", "different-affinity")
	if liveOwner(req) != first {
		t.Fatal("affinity changed actual-session ownership")
	}
	req.Headers.Set("X-Opencode-Session-Id", "child")
	if liveOwner(req) == first {
		t.Fatal("child session shared live ownership")
	}
	for _, kind := range []string{"", "title", "generate", "compaction"} {
		req.Headers.Set("X-Cpa-Request-Kind", kind)
		if liveOwner(req) != "" {
			t.Fatalf("purpose %s gained live ownership", kind)
		}
	}
	req.Headers.Set("X-Cpa-Request-Kind", "primary")
	req.Metadata = map[string]any{}
	if liveOwner(req) != "" {
		t.Fatal("unauthenticated caller gained live ownership")
	}
}

func TestLiveToolLoopCompatibilityIncludesOriginalFunctionNames(t *testing.T) {
	first := testRequest()
	first.Payload = []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`)
	second := first
	second.Payload = []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"mcp_cpa_lookup","parameters":{"type":"object"}}}]}`)
	a, err := buildRequest(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildRequest(second)
	if err != nil {
		t.Fatal(err)
	}
	if liveCompatibility(first, a) == liveCompatibility(second, b) {
		t.Fatal("different original client functions share retained compatibility")
	}
}

func TestLiveToolLoopIdentityExcludesUnverifiedDeliveryProtocols(t *testing.T) {
	req := liveRequest(nil)
	for _, path := range []string{"", "/v1/responses", "/v1/messages", "/v1/completions", "/v1/chat/completions/other"} {
		req.Metadata["request_path"] = path
		if liveOwner(req) != "" {
			t.Fatalf("unverified delivery protocol %q gained retained ownership", path)
		}
	}
	req.Metadata["request_path"] = "/v1/chat/completions"
	if liveOwner(req) == "" {
		t.Fatal("verified chat-completions delivery was rejected")
	}
}
