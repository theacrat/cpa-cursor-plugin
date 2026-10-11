package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/theacrat/cpa-cursor-plugin/internal/cursor"
)

const pluginIdentifier = "cpa-cursor"

type Plugin struct {
	mu       sync.Mutex
	shutdown bool
	quiesced bool
	cfg      cursor.Config
	auth     *cursor.AuthService
	executor *cursor.Executor
	quota    *cursor.QuotaService
	ctx      context.Context
	cancel   context.CancelFunc
	workers  sync.WaitGroup
}

func NewPlugin() *Plugin {
	cfg := cursor.DefaultConfig()
	ctx, cancel := context.WithCancel(context.Background())
	return &Plugin{cfg: cfg, auth: cursor.NewAuthService(cfg), executor: cursor.NewExecutor(cfg), quota: cursor.NewQuotaService(cfg), ctx: ctx, cancel: cancel}
}

func (p *Plugin) ApplyLifecycle(raw []byte) error {
	var req struct {
		ConfigYAML []byte `json:"config_yaml"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &req) != nil {
		return errors.New("invalid lifecycle request")
	}
	cfg, err := cursor.ParseConfig(req.ConfigYAML)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shutdown {
		return errors.New("plugin is shut down")
	}
	p.cancel()
	p.auth.Close()
	p.executor.Close()
	p.workers.Wait()
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.cfg = cfg
	p.auth = cursor.NewAuthService(cfg)
	p.executor = cursor.NewExecutor(cfg)
	p.quota = cursor.NewQuotaService(cfg)
	p.quiesced = false
	return nil
}

func (p *Plugin) stop(final bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shutdown = p.shutdown || final
	p.quiesced = true
	p.cancel()
	p.auth.Close()
	p.executor.Close()
	p.workers.Wait()
}

func (p *Plugin) Shutdown() { p.stop(true) }
func (p *Plugin) Quiesce()  { p.stop(false) }

func (p *Plugin) Registration() map[string]any {
	return map[string]any{
		"schema_version": pluginabi.SchemaVersion,
		"metadata": pluginapi.Metadata{Name: pluginIdentifier, Version: "0.2.0", Author: "thea", GitHubRepository: "https://github.com/theacrat/cpa-cursor-plugin", ConfigFields: []pluginapi.ConfigField{
			{Name: "enabled", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Enable the Cursor provider. Default true."},
			{Name: "api-base-url", Type: pluginapi.ConfigFieldTypeString, Description: "Cursor API base URL. Default https://api2.cursor.sh."},
			{Name: "login-url", Type: pluginapi.ConfigFieldTypeString, Description: "Browser login URL. Default https://cursor.com/loginDeepControl."},
			{Name: "client-version", Type: pluginapi.ConfigFieldTypeString, Description: "Cursor CLI wire compatibility version."},
			{Name: "request-timeout", Type: pluginapi.ConfigFieldTypeInteger, Description: "Upstream request deadline in seconds. Default 30."},
			{Name: "login-timeout", Type: pluginapi.ConfigFieldTypeInteger, Description: "Login session lifetime in seconds. Default 300."},
			{Name: "max-concurrent-streams", Type: pluginapi.ConfigFieldTypeInteger, Description: "Maximum concurrent inference streams. Default 32."},
		}},
		"capabilities": map[string]any{"auth_provider": true, "model_provider": true, "executor": true, "quota_provider": true, "executor_model_scope": "oauth", "executor_input_formats": []string{"chat-completions"}, "executor_output_formats": []string{"chat-completions"}},
	}
}

func (p *Plugin) dispatch(method string, raw []byte) (any, error) {
	var wire struct {
		CallbackID string `json:"host_callback_id"`
		StreamID   string `json:"stream_id"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &wire) != nil {
		return nil, errors.New("invalid request JSON")
	}
	client := hostHTTP{callbackID: wire.CallbackID}
	switch method {
	case pluginabi.MethodAuthIdentifier, pluginabi.MethodExecutorIdentifier:
		return map[string]string{"identifier": "cursor"}, nil
	case pluginabi.MethodQuotaIdentifier:
		return map[string]string{"identifier": "cursor"}, nil
	case pluginabi.MethodModelStatic:
		return pluginapi.ModelResponse{Provider: "cursor"}, nil
	case pluginabi.MethodQuotaDescribe:
		var req pluginapi.QuotaDescribeRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid quota describe request")
		}
		return p.quota.DescribeQuota(p.ctx, req)
	}
	if !p.cfg.Enabled {
		return nil, pluginabi.NewError("provider_disabled", "Cursor provider is disabled", 503)
	}
	ctx := p.ctx
	switch method {
	case pluginabi.MethodAuthParse:
		var req pluginapi.AuthParseRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid auth parse request")
		}
		return p.auth.ParseAuth(ctx, req)
	case pluginabi.MethodAuthLoginStart:
		var req pluginapi.AuthLoginStartRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid login request")
		}
		req.HTTPClient = client
		return p.auth.StartLogin(ctx, req)
	case pluginabi.MethodAuthLoginPoll:
		var req pluginapi.AuthLoginPollRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid login poll request")
		}
		req.HTTPClient = client
		return p.auth.PollLogin(ctx, req)
	case pluginabi.MethodAuthRefresh:
		var req pluginapi.AuthRefreshRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid refresh request")
		}
		req.HTTPClient = client
		return p.auth.RefreshAuth(ctx, req)
	case pluginabi.MethodModelForAuth:
		var req pluginapi.AuthModelRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid model request")
		}
		req.HTTPClient = client
		return p.executor.ModelsForAuth(ctx, req)
	case pluginabi.MethodQuotaFetch:
		var req pluginapi.QuotaFetchRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid quota fetch request")
		}
		req.HTTPClient = client
		return p.quota.FetchQuota(ctx, req)
	case pluginabi.MethodQuotaReset:
		var req pluginapi.QuotaResetRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid quota reset request")
		}
		req.HTTPClient = client
		return p.quota.ResetQuota(ctx, req)
	case pluginabi.MethodExecutorExecute, pluginabi.MethodExecutorExecuteStream, pluginabi.MethodExecutorCountTokens:
		var req pluginapi.ExecutorRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.New("invalid executor request")
		}
		req.HTTPClient = client
		if method == pluginabi.MethodExecutorCountTokens {
			return p.executor.CountTokens(ctx, req)
		}
		if method == pluginabi.MethodExecutorExecute {
			return p.executor.Execute(ctx, req)
		}
		if wire.StreamID == "" {
			return nil, errors.New("host stream ID is required")
		}
		streamCtx, cancel := context.WithCancel(ctx)
		stream, err := p.executor.ExecuteStream(streamCtx, req)
		if err != nil {
			cancel()
			return nil, err
		}
		p.workers.Add(1)
		go func() {
			defer p.workers.Done()
			defer cancel()
			defer func() {
				_ = hostDecode(pluginabi.MethodHostStreamClose, map[string]string{"stream_id": wire.StreamID}, nil)
			}()
			for chunk := range stream.Chunks {
				message := ""
				if chunk.Err != nil {
					message = chunk.Err.Error()
				}
				if err := hostDecode(pluginabi.MethodHostStreamEmit, map[string]any{"stream_id": wire.StreamID, "payload": chunk.Payload, "error": message}, nil); err != nil {
					cancel()
					for range stream.Chunks {
					}
					return
				}
			}
		}()
		return map[string]any{"headers": stream.Headers}, nil
	case pluginabi.MethodExecutorHTTPRequest:
		return nil, pluginabi.NewError("unsupported_operation", "Cursor does not support arbitrary HTTP forwarding", 400)
	default:
		return nil, pluginabi.NewError("unknown_method", "unknown method: "+method, 400)
	}
}
