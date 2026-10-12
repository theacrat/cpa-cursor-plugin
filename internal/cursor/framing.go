package cursor

import "strings"

// CPA passes chat-completions plugin chunks straight to the client handler,
// which writes its own "data: " prefix and [DONE]; framing them here would
// double the prefix. Every other client format goes through a host
// translator that requires SSE-framed input. The executor only sees its own
// output format, so the inbound request path is the sole signal.
var hostFramedPaths = []string{"/v1/chat/completions", "/v1/completions"}

func wantsSSEFraming(metadata map[string]any) bool {
	path, _ := metadata["request_path"].(string)
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	for _, p := range hostFramedPaths {
		if path == p {
			return false
		}
	}
	return true
}

func frameStreamChunk(raw []byte, framed bool) []byte {
	if !framed {
		return raw
	}
	return append(append([]byte("data: "), raw...), '\n', '\n')
}
