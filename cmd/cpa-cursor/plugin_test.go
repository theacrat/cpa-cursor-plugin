package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestPluginLifecycle(t *testing.T) {
	p := NewPlugin()
	t.Cleanup(p.Shutdown)
	if err := p.ApplyLifecycle([]byte(`{"config_yaml":"ZW5hYmxlZDogZmFsc2UK"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.dispatch("auth.login.start", []byte(`{}`)); err == nil {
		t.Fatal("disabled provider accepted login")
	}
	if _, err := p.dispatch("auth.identifier", nil); err != nil {
		t.Fatal(err)
	}
	p.Quiesce()
	if err := p.ApplyLifecycle(nil); err != nil {
		t.Fatal(err)
	}
	response, err := p.dispatch("auth.login.start", []byte(`{"Provider":"cursor"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response)
	if err != nil || len(raw) == 0 {
		t.Fatal(err)
	}
	p.Shutdown()
	if err := p.ApplyLifecycle(nil); err == nil {
		t.Fatal("shutdown plugin reactivated")
	}
}

func TestInvalidConfigKeepsCurrentLifecycle(t *testing.T) {
	p := NewPlugin()
	t.Cleanup(p.Shutdown)
	if err := p.ApplyLifecycle([]byte(`{"config_yaml":"dW5rbm93bjogdHJ1ZQo="}`)); err == nil {
		t.Fatal("unknown config accepted")
	}
	if _, err := p.dispatch("auth.login.start", []byte(`{}`)); err != nil {
		t.Fatal("invalid config stopped working provider", err)
	}
}

func TestMalformedRequestRejected(t *testing.T) {
	p := NewPlugin()
	t.Cleanup(p.Shutdown)
	if _, err := p.dispatch("auth.parse", []byte(`not-json`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if _, err := p.dispatch("executor.http_request", []byte(`{}`)); err == nil {
		t.Fatal("arbitrary forwarding accepted")
	}
}

func TestModelDispatchWithholdsUnsafeRouting(t *testing.T) {
	p := NewPlugin()
	t.Cleanup(p.Shutdown)
	p.executor.Log = nil
	response, err := p.dispatch("model.for_auth", []byte(`{"Host":{"ForceModelPrefix":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	models, ok := response.(pluginapi.ModelResponse)
	if !ok || models.Provider != "cursor" || len(models.Models) != 0 {
		t.Fatalf("unsafe routing returned %v", response)
	}
}
