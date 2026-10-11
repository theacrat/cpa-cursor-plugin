package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

var callHost = func(method string, payload any) (json.RawMessage, error) {
	return nil, fmt.Errorf("host callback is not available")
}

const (
	httpFetchTimeout = 3 * time.Second
	httpBodyLimit    = 32 << 20
)

type timeoutHTTP struct {
	timeout time.Duration
}

func newLiveHTTP() getter {
	return timeoutHTTP{timeout: httpFetchTimeout}
}

func newCatalogHTTP() getter {
	// The pinned host ABI cannot cancel HTTP callbacks or set their deadlines.
	return timeoutHTTP{timeout: httpFetchTimeout}
}

func (t timeoutHTTP) Get(url string, headers map[string]string) ([]byte, error) {
	if t.timeout <= 0 {
		t.timeout = httpFetchTimeout
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: t.timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, httpBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > httpBodyLimit {
		return nil, fmt.Errorf("GET %s: body exceeds %d bytes", url, httpBodyLimit)
	}
	return body, nil
}

func hostLog(level, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	_, _ = callHost(pluginabi.MethodHostLog, map[string]any{
		"level":   level,
		"message": "[" + pluginIdentifier + "] " + message,
	})
}
