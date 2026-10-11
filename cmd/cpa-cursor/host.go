package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var callHost = func(string, any) (json.RawMessage, error) {
	return nil, errors.New("host callback is not available")
}

type hostHTTP struct{ callbackID string }

type hostRequest struct {
	pluginapi.HTTPRequest
	CallbackID  string `json:"host_callback_id,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
}

func hostDecode(method string, request, response any) error {
	raw, err := callHost(method, request)
	if err != nil {
		return errors.New("host callback failed")
	}
	if response != nil && json.Unmarshal(raw, response) != nil {
		return errors.New("invalid host callback response")
	}
	return nil
}

// An operation is opened before Do so cancellation can interrupt the C callback
// while it is blocked in connection setup or response-body reads.
func (h hostHTTP) operation(ctx context.Context) (string, func(), error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	var opened struct {
		ID string `json:"operation_id"`
	}
	if err := hostDecode(pluginabi.MethodHostHTTPOperationOpen, map[string]string{"host_callback_id": h.callbackID}, &opened); err != nil {
		return "", nil, err
	}
	if opened.ID == "" {
		return "", nil, errors.New("host returned empty HTTP operation")
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		_ = hostDecode(pluginabi.MethodHostHTTPCancel, map[string]string{"host_callback_id": h.callbackID, "operation_id": opened.ID}, nil)
	})
	return opened.ID, func() {
		if !stop() {
			<-done
		}
	}, nil
}

func (h hostHTTP) Do(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	id, stop, err := h.operation(ctx)
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	defer stop()
	var resp pluginapi.HTTPResponse
	err = hostDecode(pluginabi.MethodHostHTTPDo, hostRequest{HTTPRequest: req, CallbackID: h.callbackID, OperationID: id}, &resp)
	if ctx.Err() != nil {
		return pluginapi.HTTPResponse{}, ctx.Err()
	}
	return resp, err
}

func (h hostHTTP) DoStream(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	id, stop, err := h.operation(ctx)
	if err != nil {
		return pluginapi.HTTPStreamResponse{}, err
	}
	var opened struct {
		Status  int         `json:"status_code"`
		Headers http.Header `json:"headers"`
		ID      string      `json:"stream_id"`
	}
	err = hostDecode(pluginabi.MethodHostHTTPDoStream, hostRequest{HTTPRequest: req, CallbackID: h.callbackID, OperationID: id}, &opened)
	if err != nil || ctx.Err() != nil {
		stop()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return pluginapi.HTTPStreamResponse{}, err
	}
	if opened.ID == "" {
		stop()
		return pluginapi.HTTPStreamResponse{}, errors.New("host returned empty HTTP stream")
	}
	chunks := make(chan pluginapi.HTTPStreamChunk)
	go func() {
		defer close(chunks)
		defer stop()
		defer func() {
			_ = hostDecode(pluginabi.MethodHostHTTPStreamClose, map[string]string{"stream_id": opened.ID}, nil)
		}()
		for {
			var read struct {
				Payload []byte `json:"payload"`
				Error   string `json:"error"`
				Done    bool   `json:"done"`
			}
			err := hostDecode(pluginabi.MethodHostHTTPStreamRead, map[string]string{"stream_id": opened.ID}, &read)
			if ctx.Err() != nil {
				return
			}
			if err == nil && read.Error != "" {
				err = errors.New("host HTTP stream failed")
			}
			if len(read.Payload) > 0 || err != nil {
				select {
				case chunks <- pluginapi.HTTPStreamChunk{Payload: read.Payload, Err: err}:
				case <-ctx.Done():
					return
				}
			}
			if read.Done || err != nil {
				return
			}
		}
	}()
	return pluginapi.HTTPStreamResponse{StatusCode: opened.Status, Headers: opened.Headers, Chunks: chunks}, nil
}
