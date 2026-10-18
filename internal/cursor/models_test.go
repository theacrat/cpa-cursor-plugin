package cursor

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestModelsRejectSharedHostRoutingBeforeHTTP(t *testing.T) {
	e := NewExecutor(DefaultConfig())
	t.Cleanup(e.Close)
	called := false
	var diagnostic string
	e.Log = func(message string) { diagnostic = message }
	req := pluginapi.AuthModelRequest{
		StorageJSON: testRequest().StorageJSON,
		HTTPClient: fixtureHost{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
			called = true
			return pluginapi.HTTPResponse{StatusCode: http.StatusServiceUnavailable}, nil
		}},
	}
	response, err := e.ModelsForAuth(context.Background(), req)
	if err != nil || !strings.Contains(diagnostic, "force-model-prefix: true") {
		t.Fatalf("unsafe routing returned models=%v error=%v", response.Models, err)
	}
	if called || len(response.Models) != 0 || response.Provider != "cursor" {
		t.Fatal("unsafe host policy reached discovery or published models")
	}
}

func TestDiscoveryUsesControlPlaneDeadline(t *testing.T) {
	e := NewExecutor(Config{Enabled: true, RequestTimeout: 1, ExecutionTimeout: 600})
	t.Cleanup(e.Close)
	called := false
	req := pluginapi.AuthModelRequest{Host: pluginapi.HostConfigSummary{ForceModelPrefix: true}, StorageJSON: testRequest().StorageJSON}
	req.HTTPClient = fixtureHost{do: func(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		called = true
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Errorf("discovery ignored its one-second request timeout: %v", deadline)
		}
		return pluginapi.HTTPResponse{StatusCode: http.StatusServiceUnavailable}, nil
	}}
	if _, err := e.ModelsForAuth(context.Background(), req); err == nil || !called {
		t.Fatalf("discovery called=%v error=%v", called, err)
	}
}

func TestDisambiguateEffortVariants(t *testing.T) {
	in := []pluginapi.ModelInfo{
		{ID: "claude-opus-4-8-high", DisplayName: "Claude Opus 4.8"},
		{ID: "claude-opus-4-8-low", DisplayName: "Claude Opus 4.8"},
		{ID: "claude-4.5-sonnet", DisplayName: "Claude Sonnet 4.5"},
		{ID: "claude-4.5-sonnet-thinking", DisplayName: "Claude Sonnet 4.5"},
		{ID: "solo-model", DisplayName: "Solo"},
		{ID: "weird-a", DisplayName: "Weird"},
		{ID: "weird-b", DisplayName: "Weird"},
	}
	disambiguateDisplayNames(in)
	want := []string{
		"Claude Opus 4.8 [high]",
		"Claude Opus 4.8 [low]",
		"Claude Sonnet 4.5 [claude-4.5-sonnet]",
		"Claude Sonnet 4.5 [thinking]",
		"Solo",
		"Weird [weird-a]",
		"Weird [weird-b]",
	}
	for i, name := range want {
		if in[i].DisplayName != name {
			t.Fatalf("row %d: got %q want %q", i, in[i].DisplayName, name)
		}
	}
}
