package cursor

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

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
