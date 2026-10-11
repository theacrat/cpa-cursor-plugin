package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const maxJSONResponseBytes = 2 << 20

// HTTPStatusError deliberately excludes URLs, headers and response bodies.
type HTTPStatusError struct{ StatusCode int }

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("cursor upstream returned HTTP %d", e.StatusCode)
}

// DoJSON uses only the host transport. No process-wide HTTP fallback can bypass
// host proxy policy or request logging. Non-2xx responses are never decoded.
func DoJSON(ctx context.Context, client pluginapi.HostHTTPClient, req pluginapi.HTTPRequest, timeout time.Duration, out any) error {
	if client == nil {
		return errors.New("cursor host HTTP client is required")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	resp, err := client.Do(ctx, req)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errors.New("cursor upstream transport failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPStatusError{StatusCode: resp.StatusCode}
	}
	if len(resp.Body) > maxJSONResponseBytes {
		return errors.New("cursor upstream JSON response is too large")
	}
	if out != nil && json.Unmarshal(resp.Body, out) != nil {
		return errors.New("cursor upstream returned invalid JSON")
	}
	return nil
}
