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

// Connect codes are protocol-defined, not upstream messages.
func connectStatus(code string) error {
	status := map[string]int{"unauthenticated": 401, "permission_denied": 403, "resource_exhausted": 429, "unavailable": 503, "internal": 500, "unknown": 500, "deadline_exceeded": 504, "invalid_argument": 400, "not_found": 404, "unimplemented": 501, "canceled": 499, "already_exists": 409, "aborted": 409, "failed_precondition": 400, "out_of_range": 400, "data_loss": 500}[code]
	if status == 0 {
		return errors.New("cursor upstream Connect error")
	}
	return &HTTPStatusError{StatusCode: status}
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
