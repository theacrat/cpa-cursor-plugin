//go:build cgo

package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestUpstreamErrorEnvelopeRetryPolicy(t *testing.T) {
	for _, status := range []int{400, 401, 403, 408, 429, 500, 503} {
		raw, err := upstreamErrorEnvelope("safe upstream failure", status)
		if err != nil {
			t.Fatal(err)
		}
		var envelope pluginabi.Envelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.OK || envelope.Error == nil || envelope.Error.HTTPStatus != status {
			t.Fatalf("%s", raw)
		}
		want := status == 408 || status == 429 || status >= 500
		if envelope.Error.Retryable != want {
			t.Fatalf("status %d retryable=%v want=%v", status, envelope.Error.Retryable, want)
		}
	}
}
