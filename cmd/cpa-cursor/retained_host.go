package main

import (
	"context"
	"errors"

	"github.com/theacrat/cpa-cursor-plugin/internal/cursor"
)

type retainedHostScope struct {
	hostHTTP
	callback   string
	attachment string
}

func (h hostHTTP) SupportsRetainedHTTP() bool { return h.retained }

func (h hostHTTP) OpenScope(ctx context.Context) (cursor.RetainedHTTPScope, error) {
	return h.scope(ctx, "host.http.scope_open", "")
}

func (h hostHTTP) AttachScope(ctx context.Context, id string) (cursor.RetainedHTTPScope, error) {
	return h.scope(ctx, "host.http.scope_attach", id)
}

func (h hostHTTP) scope(ctx context.Context, method, id string) (cursor.RetainedHTTPScope, error) {
	if !h.retained || h.callbackID == "" {
		return nil, errors.New("retained HTTP is unsupported by this host")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request := map[string]any{"host_callback_id": h.callbackID}
	if id != "" {
		request["scope_id"] = id
	} else {
		request["idle_timeout_ms"] = 3600000
	}
	var response struct {
		ID         string `json:"scope_id"`
		Attachment string `json:"attachment_id"`
	}
	if err := hostDecode(method, request, &response); err != nil {
		return nil, err
	}
	if id != "" && response.ID == "" {
		response.ID = id
	}
	if response.ID == "" || response.Attachment == "" {
		return nil, errors.New("host returned an invalid retained scope")
	}
	if err := ctx.Err(); err != nil {
		_ = hostDecode("host.http.scope_close", map[string]string{"scope_id": response.ID, "attachment_id": response.Attachment}, nil)
		return nil, err
	}
	return &retainedHostScope{hostHTTP: hostHTTP{workers: h.workers, retained: true, scopeID: response.ID}, callback: h.callbackID, attachment: response.Attachment}, nil
}

func (h *retainedHostScope) ID() string { return h.scopeID }

func (h *retainedHostScope) PreparePark(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return hostDecode("host.http.scope_prepare_park", map[string]string{"host_callback_id": h.callback, "scope_id": h.scopeID, "attachment_id": h.attachment}, nil)
}

func (h *retainedHostScope) Close() error {
	return hostDecode("host.http.scope_close", map[string]string{"scope_id": h.scopeID, "attachment_id": h.attachment}, nil)
}
