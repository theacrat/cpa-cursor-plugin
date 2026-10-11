//go:build cgo

package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestABIDispatchLifecycle(t *testing.T) {
	abiLifecycleMu.Lock()
	drainDispatch()
	plugin.Shutdown()
	plugin = NewPlugin()
	resumeDispatch()
	abiLifecycleMu.Unlock()
	t.Cleanup(func() { _, _ = handleMethod(pluginabi.MethodPluginShutdown, nil) })
	for _, method := range []string{pluginabi.MethodPluginRegister, pluginabi.MethodAuthIdentifier, pluginabi.MethodPluginQuiesce, pluginabi.MethodPluginReconfigure, pluginabi.MethodPluginShutdown} {
		raw, err := handleMethod(method, nil)
		if err != nil {
			t.Fatal(err)
		}
		var env pluginabi.Envelope
		if json.Unmarshal(raw, &env) != nil || !env.OK {
			t.Fatalf("%s: %s", method, raw)
		}
	}
	raw, err := handleMethod(pluginabi.MethodAuthIdentifier, nil)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if json.Unmarshal(raw, &env) != nil || env.OK || env.Error.Code != "plugin_shutdown" {
		t.Fatalf("shutdown accepted call: %s", raw)
	}
}

func TestABIInvalidReconfigurePreservesProvider(t *testing.T) {
	abiLifecycleMu.Lock()
	drainDispatch()
	plugin.Shutdown()
	plugin = NewPlugin()
	resumeDispatch()
	abiLifecycleMu.Unlock()
	t.Cleanup(func() { _, _ = handleMethod(pluginabi.MethodPluginShutdown, nil) })
	raw, err := handleMethod(pluginabi.MethodPluginReconfigure, []byte(`{"config_yaml":"dW5rbm93bjogdHJ1ZQo="}`))
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if json.Unmarshal(raw, &env) != nil || env.OK {
		t.Fatal(string(raw))
	}
	raw, err = handleMethod(pluginabi.MethodAuthLoginStart, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &env) != nil || !env.OK {
		t.Fatalf("invalid reconfigure cancelled provider: %s", raw)
	}
}
