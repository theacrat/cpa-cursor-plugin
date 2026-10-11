package cursor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type fakeHTTP struct {
	do func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error)
}

func (f fakeHTTP) Do(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	return f.do(ctx, req)
}
func (f fakeHTTP) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	panic("unary requests must not stream")
}

func TestDoJSON(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		transport error
		wantErr   bool
	}{
		{"success", 200, `{"ok":true}`, nil, false}, {"status", 401, "secret", nil, true}, {"malformed", 200, "secret", nil, true}, {"large", 200, strings.Repeat("s", maxJSONResponseBytes+1), nil, true}, {"transport", 0, "", errors.New("secret token verifier URL"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			err := DoJSON(context.Background(), fakeHTTP{do: func(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing deadline")
				}
				return pluginapi.HTTPResponse{StatusCode: tc.status, Body: []byte(tc.body)}, tc.transport
			}}, pluginapi.HTTPRequest{}, time.Second, &got)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
	if err := DoJSON(context.Background(), nil, pluginapi.HTTPRequest{}, time.Second, nil); err == nil {
		t.Fatal("missing host transport accepted")
	}
}

func TestDoJSONCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := fakeHTTP{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		t.Fatal("called cancelled request")
		return pluginapi.HTTPResponse{}, nil
	}}
	if err := DoJSON(ctx, client, pluginapi.HTTPRequest{}, time.Second, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	client.do = func(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		<-ctx.Done()
		return pluginapi.HTTPResponse{}, errors.New("secret")
	}
	if err := DoJSON(context.Background(), client, pluginapi.HTTPRequest{}, time.Millisecond, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
}
