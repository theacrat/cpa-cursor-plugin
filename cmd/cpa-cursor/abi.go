//go:build cgo

package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/theacrat/cpa-cursor-plugin/internal/cursor"
)

// Lifecycle changes close admission, cancel upstream operations, then drain
// dispatch and async stream workers before replacing the instance.
var abiLifecycleMu sync.Mutex
var dispatchMu sync.Mutex
var dispatchCond = sync.NewCond(&dispatchMu)
var dispatchActive int
var dispatchStopped bool

func drainDispatch() {
	dispatchMu.Lock()
	dispatchStopped = true
	dispatchCond.Broadcast()
	for dispatchActive != 0 {
		dispatchCond.Wait()
	}
	dispatchMu.Unlock()
}

func resumeDispatch() {
	dispatchMu.Lock()
	dispatchStopped = false
	dispatchMu.Unlock()
}

func acquireDispatch() (*Plugin, bool) {
	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	if dispatchStopped {
		return nil, false
	}
	dispatchActive++
	return plugin, true
}

func releaseDispatch() {
	dispatchMu.Lock()
	dispatchActive--
	dispatchCond.Broadcast()
	dispatchMu.Unlock()
}

// The host barrier covers callbacks through copying and freeing their result.
var hostMu sync.RWMutex
var hostStopped bool

func stopHostCallbacks() {
	hostMu.Lock()
	hostStopped = true
	hostMu.Unlock()
}

func init() {
	callHost = cgoCallHost
}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, out *C.cliproxy_plugin_api) C.int {
	if out == nil || host == nil || host.abi_version != C.uint32_t(pluginabi.ABIVersion) || host.call == nil || host.free_buffer == nil {
		return 1
	}
	abiLifecycleMu.Lock()
	defer abiLifecycleMu.Unlock()
	plugin.cancel()
	drainDispatch()
	plugin.Shutdown()
	hostMu.Lock()
	C.store_host_api(host)
	hostStopped = false
	hostMu.Unlock()
	plugin = NewPlugin()
	resumeDispatch()
	out.abi_version = C.uint32_t(pluginabi.ABIVersion)
	out.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	out.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	out.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) (ret C.int) {
	defer func() {
		if recover() != nil {
			writeResponse(response, errorEnvelope("plugin_panic", "plugin encountered an internal error"))
			ret = 1
		}
	}()
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	if requestLen > 32<<20 || (requestLen > 0 && request == nil) {
		writeResponse(response, errorEnvelope("invalid_request", "invalid or oversized request buffer"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	abiLifecycleMu.Lock()
	defer abiLifecycleMu.Unlock()
	plugin.cancel()
	drainDispatch()
	plugin.Shutdown()
	stopHostCallbacks()
	hostMu.Lock()
	C.store_host_api(nil)
	hostMu.Unlock()
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		abiLifecycleMu.Lock()
		defer abiLifecycleMu.Unlock()
		var config struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if len(request) > 0 && json.Unmarshal(request, &config) != nil {
			return errorEnvelope("invalid_config", "invalid lifecycle request"), nil
		}
		if _, err := cursor.ParseConfig(config.ConfigYAML); err != nil {
			return errorEnvelope("invalid_config", err.Error()), nil
		}
		plugin.cancel()
		drainDispatch()
		plugin.mu.Lock()
		shutdown := plugin.shutdown
		plugin.mu.Unlock()
		if shutdown {
			return errorEnvelope("plugin_shutdown", "plugin is shut down"), nil
		}
		hostMu.Lock()
		hostStopped = false
		hostMu.Unlock()
		if err := plugin.ApplyLifecycle(request); err != nil {
			resumeDispatch()
			return errorEnvelope("invalid_config", err.Error()), nil
		}
		resumeDispatch()
		return okEnvelope(plugin.Registration())
	case pluginabi.MethodPluginShutdown:
		abiLifecycleMu.Lock()
		defer abiLifecycleMu.Unlock()
		plugin.cancel()
		drainDispatch()
		plugin.Shutdown()
		stopHostCallbacks()
		return okEnvelope(map[string]any{})
	case pluginabi.MethodPluginQuiesce:
		abiLifecycleMu.Lock()
		defer abiLifecycleMu.Unlock()
		plugin.cancel()
		drainDispatch()
		plugin.Quiesce()
		stopHostCallbacks()
		return okEnvelope(map[string]any{})
	}
	p, admitted := acquireDispatch()
	if !admitted {
		return errorEnvelope("plugin_shutdown", "plugin dispatch is stopped"), nil
	}
	defer releaseDispatch()
	value, err := p.dispatch(method, request)
	if err != nil {
		var upstream *pluginabi.Error
		if errors.As(err, &upstream) {
			return json.Marshal(pluginabi.Envelope{OK: false, Error: upstream})
		}
		var status interface{ StatusCode() int }
		if errors.As(err, &status) {
			return json.Marshal(pluginabi.Envelope{OK: false, Error: pluginabi.NewError("upstream_error", err.Error(), status.StatusCode())})
		}
		var authStatus *cursor.HTTPStatusError
		if errors.As(err, &authStatus) {
			return json.Marshal(pluginabi.Envelope{OK: false, Error: pluginabi.NewError("upstream_error", err.Error(), authStatus.StatusCode)})
		}
		return errorEnvelope("plugin_error", err.Error()), nil
	}
	return okEnvelope(value)
}

func okEnvelope(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginabi.Envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(pluginabi.Envelope{
		OK: false,
		Error: &pluginabi.Error{
			Code:    code,
			Message: message,
		},
	})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

func cgoCallHost(method string, payload any) (json.RawMessage, error) {
	hostMu.RLock()
	defer hostMu.RUnlock()
	if hostStopped {
		return nil, fmt.Errorf("host callbacks are stopped")
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host callback payload %s: %w", method, err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmt.Errorf("allocate host callback payload %s", method)
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}
	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil && response.len > 0 {
		if response.len > 32<<20 {
			C.free_host_buffer(response.ptr, response.len)
			return nil, fmt.Errorf("host callback response exceeds size limit")
		}
		rawResponse = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(callCode))
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(rawResponse, &env); err != nil {
		return nil, fmt.Errorf("decode host callback envelope %s: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s", env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if callCode != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(callCode))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}
