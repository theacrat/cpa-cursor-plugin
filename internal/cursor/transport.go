package cursor

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pb "github.com/theacrat/cpa-cursor-plugin/internal/proto"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type runTransport struct {
	reader io.Reader
	send   func(*pb.AgentClientMessage) error
	close  func()
}

func endpoint(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("Cursor endpoint must be an HTTPS URL without credentials, query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String(), nil
}
func (e *Executor) headers(storage []byte, metadata map[string]any, stream bool, id string) (http.Header, error) {
	token, err := executorToken(storage, metadata)
	if err != nil {
		return nil, err
	}
	version := e.cfg.ClientVersion
	if version == "" {
		version = "cli-2026.10.01-e373342"
	}
	h := http.Header{"Authorization": []string{"Bearer " + token}, "Connect-Protocol-Version": []string{"1"}, "X-Ghost-Mode": []string{"true"}, "X-Cursor-Client-Version": []string{version}, "X-Cursor-Client-Type": []string{"cli"}, "X-Request-Id": []string{id}, "Content-Type": []string{"application/proto"}}
	var data map[string]any
	_ = json.Unmarshal(storage, &data)
	team := teamHeader(data["selected_team_id"])
	if team == "" {
		team = teamHeader(metadata["selected_team_id"])
	}
	if team != "" {
		if strings.ContainsAny(team, "\r\n") {
			return nil, errors.New("invalid Cursor team ID")
		}
		h.Set("X-Cursor-Team-Id", team)
	}
	if stream {
		h.Set("Content-Type", "application/connect+proto")
		h.Set("Connect-Accept-Encoding", "gzip")
	}
	return h, nil
}

func teamHeader(value any) string {
	switch v := value.(type) {
	case float64:
		if v > 0 && v <= 9007199254740991 && v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
	case int64:
		if v > 0 {
			return strconv.FormatInt(v, 10)
		}
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return strconv.FormatInt(n, 10)
		}
	}
	return ""
}
func frameMessage(m proto.Message) ([]byte, error) {
	raw, err := proto.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxFrame {
		return nil, errors.New("Cursor outbound frame exceeds limit")
	}
	frame := make([]byte, len(raw)+5)
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(raw)))
	copy(frame[5:], raw)
	return frame, nil
}
func readFrame(r io.Reader, encoding string) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	flags := header[0]
	if flags & ^byte(3) != 0 {
		return 0, nil, errors.New("unsupported Cursor Connect frame flags")
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > maxFrame {
		return 0, nil, errors.New("Cursor Connect frame exceeds limit")
	}
	raw := make([]byte, int(size))
	if _, err := io.ReadFull(r, raw); err != nil {
		return 0, nil, err
	}
	if flags&1 != 0 {
		if encoding != "gzip" {
			return 0, nil, errors.New("unsupported Cursor Connect compression")
		}
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return 0, nil, errors.New("invalid Cursor compressed frame")
		}
		defer gz.Close()
		raw, err = io.ReadAll(io.LimitReader(gz, maxFrame+1))
		if err != nil {
			return 0, nil, errors.New("invalid Cursor compressed frame")
		}
		if len(raw) > maxFrame {
			return 0, nil, errors.New("Cursor decompressed frame exceeds limit")
		}
	}
	return flags, raw, nil
}

type frameReader struct {
	io.Reader
	encoding string
}

// RunSSE plus BidiAppend is Cursor CLI's fixed-body duplex emulation. It
// preserves CPA transport policy; sequence numbers are serialised per run.
func (e *Executor) hostRun(ctx context.Context, req pluginapi.ExecutorRequest, b *builtRequest) (*runTransport, error) {
	ctx, cancel := context.WithCancel(ctx)
	ready := false
	defer func() {
		if !ready {
			cancel()
		}
	}()
	base := e.cfg.APIBaseURL
	if base == "" {
		base = "https://api2.cursor.sh"
	}
	api := e.cfg.APIBaseURL
	if api == "" {
		api = "https://api2.cursor.sh"
	}
	runURL, err := endpoint(base, "/agent.v1.AgentService/RunSSE")
	if err != nil {
		return nil, err
	}
	appendURL, err := endpoint(api, "/aiserver.v1.BidiService/BidiAppend")
	if err != nil {
		return nil, err
	}
	id := uuid()
	h, err := e.headers(req.StorageJSON, req.AuthMetadata, true, id)
	if err != nil {
		return nil, err
	}
	h.Set("X-Cursor-Streaming", "true")
	bidiID := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), id)
	requestBody := make([]byte, 5+len(bidiID))
	binary.BigEndian.PutUint32(requestBody[1:5], uint32(len(bidiID)))
	copy(requestBody[5:], bidiID)
	stream, err := req.HTTPClient.DoStream(ctx, pluginapi.HTTPRequest{Method: http.MethodPost, URL: runURL, Headers: h, Body: requestBody})
	if err != nil {
		return nil, errors.New("Cursor RunSSE transport failed")
	}
	if stream.StatusCode < 200 || stream.StatusCode >= 300 {
		return nil, fmt.Errorf("Cursor RunSSE returned HTTP %d", stream.StatusCode)
	}
	pr, pw := io.Pipe()
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		defer pw.Close()
		for {
			select {
			case <-ctx.Done():
				_ = pw.CloseWithError(ctx.Err())
				return
			case c, ok := <-stream.Chunks:
				if !ok {
					return
				}
				if c.Err != nil {
					_ = pw.CloseWithError(errors.New("Cursor RunSSE stream failed"))
					return
				}
				if len(c.Payload) > maxFrame+5 {
					_ = pw.CloseWithError(errors.New("Cursor host stream chunk exceeds limit"))
					return
				}
				if _, err := pw.Write(c.Payload); err != nil {
					return
				}
			}
		}
	}()
	var mu sync.Mutex
	var seq uint64
	send := func(msg *pb.AgentClientMessage) error {
		mu.Lock()
		defer mu.Unlock()
		raw, err := proto.Marshal(msg)
		if err != nil {
			return err
		}
		if len(raw) > maxFrame {
			return errors.New("Cursor append exceeds limit")
		}
		// Official field 1 is hex-encoded protobuf, field 2 is BidiRequestId.
		body := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), hex.EncodeToString(raw))
		body = protowire.AppendBytes(protowire.AppendTag(body, 2, protowire.BytesType), bidiID)
		body = protowire.AppendVarint(protowire.AppendTag(body, 3, protowire.VarintType), seq)
		headers := h.Clone()
		headers.Set("Content-Type", "application/proto")
		resp, err := req.HTTPClient.Do(ctx, pluginapi.HTTPRequest{Method: http.MethodPost, URL: appendURL, Headers: headers, Body: body})
		if err != nil {
			return errors.New("Cursor BidiAppend transport failed")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("Cursor BidiAppend returned HTTP %d", resp.StatusCode)
		}
		seq++
		return nil
	}
	t := &runTransport{reader: frameReader{Reader: pr, encoding: stream.Headers.Get("Connect-Content-Encoding")}, send: send, close: func() { cancel(); _ = pr.Close(); _ = pw.Close(); <-pumpDone }}
	if err := send(b.message); err != nil {
		t.close()
		return nil, err
	}
	ready = true
	return t, nil
}

func (e *Executor) nativeRun(ctx context.Context, req pluginapi.ExecutorRequest, b *builtRequest) (*runTransport, error) {
	base := e.cfg.AgentBaseURL
	if base == "" {
		base = "https://api2.cursor.sh"
	}
	runURL, err := endpoint(base, "/agent.v1.AgentService/Run")
	if err != nil {
		return nil, err
	}
	h, err := e.headers(req.StorageJSON, req.AuthMetadata, true, uuid())
	if err != nil {
		return nil, err
	}
	client := e.client
	var transport *http.Transport
	if client == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = true
		transport.ResponseHeaderTimeout = 30 * time.Second
		proxy := req.AuthAttributes["proxy_url"]
		if proxy == "" {
			proxy, _ = req.AuthMetadata["proxy_url"].(string)
		}
		if proxy == "" {
			proxy, _ = req.Metadata["proxy_url"].(string)
		}
		if proxy == "direct" {
			transport.Proxy = nil
		} else if proxy != "" {
			p, err := url.Parse(proxy)
			if err != nil || p.Host == "" || (p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5" && p.Scheme != "socks5h") {
				return nil, errors.New("unsupported Cursor proxy URL")
			}
			transport.Proxy = http.ProxyURL(p)
		}
		if err := http2.ConfigureTransport(transport); err != nil {
			return nil, errors.New("Cursor HTTP/2 transport configuration failed")
		}
		client = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	pr, pw := io.Pipe()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, runURL, pr)
	if err != nil {
		return nil, err
	}
	request.Header = h
	initial, err := frameMessage(b.message)
	if err != nil {
		return nil, err
	}
	initialDone := make(chan error, 1)
	go func() { _, err := pw.Write(initial); initialDone <- err }()
	resp, err := client.Do(request)
	if err != nil {
		_ = pr.Close()
		_ = pw.Close()
		<-initialDone
		if transport != nil {
			transport.CloseIdleConnections()
		}
		return nil, errors.New("Cursor native HTTP/2 transport failed")
	}
	cleanup := func() {
		_ = resp.Body.Close()
		_ = pr.Close()
		_ = pw.Close()
		if transport != nil {
			transport.CloseIdleConnections()
		}
	}
	if resp.ProtoMajor != 2 {
		cleanup()
		<-initialDone
		return nil, errors.New("Cursor native transport requires HTTP/2 TLS; use host RunSSE behind HTTP/1-only proxies")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		cleanup()
		<-initialDone
		return nil, fmt.Errorf("Cursor Run returned HTTP %d", resp.StatusCode)
	}
	if err := <-initialDone; err != nil {
		cleanup()
		return nil, errors.New("Cursor initial request write failed")
	}
	var mu sync.Mutex
	return &runTransport{reader: frameReader{Reader: resp.Body, encoding: resp.Header.Get("Connect-Content-Encoding")}, send: func(m *pb.AgentClientMessage) error {
		raw, err := frameMessage(m)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		_, err = pw.Write(raw)
		return err
	}, close: cleanup}, nil
}
