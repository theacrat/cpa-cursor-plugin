package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func fixtureJWT(sub string, expires time.Time) string {
	b, _ := json.Marshal(map[string]any{"sub": sub, "exp": expires.Unix(), "email": "person@example.com"})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(b) + ".not-a-signature"
}

func fixtureService(t *testing.T) (*AuthService, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	s := NewAuthService(DefaultConfig())
	s.now = func() time.Time { return now }
	t.Cleanup(func() { _ = s.Close() })
	return s, &now
}

func TestParseAuth(t *testing.T) {
	s, now := fixtureService(t)
	raw, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": fixtureJWT("user-1", now.Add(time.Hour)), "refresh_token": "refresh-secret", "prefix": "personal", "proxy_url": "http://proxy", "disabled": true, "metadata": map[string]any{"note": "kept"}, "attributes": map[string]string{"route": "kept"}, "extra": "kept"})
	r, err := s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw, FileName: "cursor.json"})
	if err != nil || !r.Handled {
		t.Fatalf("parse: %#v %v", r, err)
	}
	a := r.Auth
	if a.Provider != "cursor" || a.ID == "" || a.FileName != "cursor.json" || !a.Disabled || a.Prefix != "personal" || a.ProxyURL != "http://proxy" || a.Metadata["note"] != "kept" || a.Attributes["route"] != "kept" || !a.NextRefreshAfter.Equal(now.Add(55*time.Minute)) {
		t.Fatalf("auth: %#v", a)
	}
	if !strings.Contains(string(a.StorageJSON), `"extra":"kept"`) {
		t.Fatal("extra storage dropped")
	}
	for _, tc := range []struct {
		raw           string
		handled, fail bool
	}{
		{`{"type":"other","access_token":17}`, false, false}, {`{"type":"cursor"}`, true, true}, {`{"type":"cursor","access_token":17}`, true, true}, {`{"type":"cursor","access_token":"opaque"}`, true, true}, {`{"type":"cursor","access_token":"opaque","id":"fixed"}`, true, false}, {`{"type":"cursor","access_token":"opaque","refresh_token":"r","expires_at":"invalid-secret"}`, true, true}, {`{"type":"cursor","access_token":"opaque","refresh_token":"r","disabled":"true"}`, true, true}, {`{"type":"cursor","access_token":"opaque","refresh_token":"r","selected_team_id":0}`, true, true}, {`broken-secret`, false, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			r, err := s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: []byte(tc.raw)})
			if r.Handled != tc.handled || (err != nil) != tc.fail {
				t.Fatalf("handled %v error %v", r.Handled, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
	r, err = s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{Provider: "other", RawJSON: []byte("bad")})
	if err != nil || r.Handled {
		t.Fatal("handled foreign provider")
	}
}

func TestAuthFileNameAlwaysHasJSONSuffix(t *testing.T) {
	s, now := fixtureService(t)
	token := fixtureJWT("user-1", now.Add(time.Hour))
	// Login-produced records carry no file name; the host would otherwise
	// persist them under the bare auth ID, which its *.json rescan skips.
	raw, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": token, "refresh_token": "refresh-secret"})
	r, err := s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw})
	if err != nil || !r.Handled {
		t.Fatalf("parse: %#v %v", r, err)
	}
	if r.Auth.FileName != r.Auth.ID+".json" {
		t.Fatalf("file name = %q, want %q", r.Auth.FileName, r.Auth.ID+".json")
	}
	// Stored names are sanitised to stay inside the auth directory.
	raw, _ = json.Marshal(map[string]any{"type": "cursor", "access_token": token, "refresh_token": "r", "file_name": "../../evil"})
	r, err = s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw})
	if err != nil || r.Auth.FileName != "evil.json" {
		t.Fatalf("sanitised file name = %#v %v", r.Auth, err)
	}
	// Host-provided names keep working and gain the suffix when missing.
	r, err = s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw, FileName: "host-name"})
	if err != nil || r.Auth.FileName != "host-name.json" {
		t.Fatalf("host file name = %#v %v", r.Auth, err)
	}
}

func TestAuthModelPrefixPolicy(t *testing.T) {
	s, now := fixtureService(t)
	raw, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": fixtureJWT("user-1", now.Add(time.Hour)), "refresh_token": "r", "prefix": ""})
	r, err := s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw})
	if err != nil || r.Auth.Prefix != "cursor" {
		t.Fatalf("empty stored prefix should take the configured default: %q %v", r.Auth.Prefix, err)
	}
	if strings.Contains(string(r.Auth.StorageJSON), `"prefix":"cursor"`) {
		t.Fatal("configured default must not be written into credential storage")
	}
	cfg := DefaultConfig()
	cfg.ModelPrefix = "work"
	if err := s.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r, err = s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw})
	if err != nil || r.Auth.Prefix != "work" {
		t.Fatalf("configured prefix not applied: %q %v", r.Auth.Prefix, err)
	}
	keyed, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": "old-opaque", "api_key": "dashboard-secret"})
	client := fakeHTTP{do: func(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		body, _ := json.Marshal(map[string]any{"accessToken": fixtureJWT("user-1", now.Add(time.Hour))})
		return pluginapi.HTTPResponse{StatusCode: 200, Body: body}, nil
	}}
	// Refresh leaves an unstored prefix empty so CPA keeps the auth's current
	// one, which an operator may have changed on the host.
	refreshed, err := s.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{StorageJSON: keyed, HTTPClient: client})
	if err != nil || refreshed.Auth.Prefix != "" {
		t.Fatalf("refresh must not impose the configured prefix: %q %v", refreshed.Auth.Prefix, err)
	}
	for _, bad := range []string{"a/b", "has space", "../x"} {
		raw, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": "opaque", "id": "fixed", "prefix": bad})
		if _, err := s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw}); err == nil {
			t.Fatalf("malformed stored prefix %q accepted", bad)
		}
	}
}

func TestLoginPKCEPendingSuccessReplay(t *testing.T) {
	s, now := fixtureService(t)
	start, err := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(start.URL)
	q := u.Query()
	if q.Get("mode") != "login" || q.Get("redirectTarget") != "cli" || q.Get("supportsSelectedTeamLogin") != "true" || len(q.Get("uuid")) != 36 || start.State == q.Get("uuid") || len(start.Metadata) != 0 || q.Get("verifier") != "" {
		t.Fatalf("unsafe login response: %#v", start)
	}
	count := 0
	client := fakeHTTP{do: func(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		count++
		p, _ := url.Parse(req.URL)
		verifier := p.Query().Get("verifier")
		b, _ := base64.RawURLEncoding.DecodeString(verifier)
		h := sha256.Sum256([]byte(verifier))
		if len(b) != 32 || base64.RawURLEncoding.EncodeToString(h[:]) != q.Get("challenge") || p.Query().Get("uuid") != q.Get("uuid") || p.Path != "/auth/poll" || req.Method != http.MethodGet {
			t.Fatal("invalid PKCE poll")
		}
		if count == 1 {
			return pluginapi.HTTPResponse{StatusCode: 404}, nil
		}
		body, _ := json.Marshal(map[string]any{"accessToken": fixtureJWT("user-1", now.Add(time.Hour)), "refreshToken": "refresh-secret", "selectedTeamId": 42})
		return pluginapi.HTTPResponse{StatusCode: 200, Body: body}, nil
	}}
	req := pluginapi.AuthLoginPollRequest{State: start.State, HTTPClient: client}
	r, err := s.PollLogin(context.Background(), req)
	if err != nil || r.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("%#v %v", r, err)
	}
	r, err = s.PollLogin(context.Background(), req)
	if err != nil || r.Status != pluginapi.AuthLoginStatusPending || count != 1 {
		t.Fatal("poll was not throttled")
	}
	*now = now.Add(time.Second)
	r, err = s.PollLogin(context.Background(), req)
	if err != nil || r.Status != pluginapi.AuthLoginStatusSuccess || !strings.Contains(string(r.Auth.StorageJSON), `"selected_team_id":42`) || r.Auth.Prefix != "cursor" {
		t.Fatalf("%#v %v", r, err)
	}
	r, err = s.PollLogin(context.Background(), req)
	if err != nil || r.Status != pluginapi.AuthLoginStatusError || count != 2 {
		t.Fatal("replayed successful login")
	}
}

func TestLoginExpiryCapacityRandomAndReload(t *testing.T) {
	s, now := fixtureService(t)
	var first string
	for i := 0; i < maxLoginSessions; i++ {
		start, err := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = start.State
		}
	}
	if _, err := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{}); err == nil {
		t.Fatal("unbounded sessions")
	}
	*now = now.Add(300 * time.Second)
	r, err := s.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: first})
	if err != nil || r.Status != pluginapi.AuthLoginStatusError {
		t.Fatal("expiry ignored")
	}
	start, err := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{})
	if err != nil || len(s.sessions) != 1 {
		t.Fatal("expired sessions not evicted")
	}
	if err := s.SetConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	r, err = s.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: start.State})
	if err != nil || r.Status != pluginapi.AuthLoginStatusError {
		t.Fatal("reload retained login state")
	}
	s.random = strings.NewReader("")
	if _, err := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{}); err == nil {
		t.Fatal("ignored RNG failure")
	}
	_ = s.Close()
	if _, err := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{}); err == nil {
		t.Fatal("closed service accepted request")
	}
	if err := s.SetConfig(DefaultConfig()); err == nil {
		t.Fatal("closed service reopened")
	}
}

func TestRefreshAPIKeyPreservesIdentityAndHostFields(t *testing.T) {
	s, now := fixtureService(t)
	raw, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": "old-opaque", "api_key": "dashboard-secret", "refresh_token": "browser-secret", "prefix": "personal", "disabled": true, "proxy_url": "http://proxy", "metadata": map[string]any{"old": true}, "extra": "kept", "selected_team_id": 42})
	client := fakeHTTP{do: func(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		if req.Headers.Get("Authorization") != "Bearer dashboard-secret" || req.Method != "POST" || !strings.HasSuffix(req.URL, "/auth/exchange_user_api_key") || string(req.Body) != "{}" {
			t.Fatal("incorrect API key exchange")
		}
		body, _ := json.Marshal(map[string]any{"accessToken": fixtureJWT("new-sub", now.Add(2*time.Hour))})
		return pluginapi.HTTPResponse{StatusCode: 200, Body: body}, nil
	}}
	parsed, err := s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{StorageJSON: parsed.Auth.StorageJSON, HTTPClient: client, AuthID: "host-fixed-id", Metadata: map[string]any{"host": true}, Attributes: map[string]string{"route": "host"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Auth.ID != "host-fixed-id" || !r.Auth.Disabled || r.Auth.Prefix != "personal" || r.Auth.ProxyURL != "http://proxy" || r.Auth.Metadata["old"] != true || r.Auth.Metadata["host"] != true || r.Auth.Attributes["route"] != "host" || !r.NextRefreshAfter.Equal(now.Add(115*time.Minute)) {
		t.Fatalf("refresh fields: %#v", r)
	}
	var storage map[string]any
	_ = json.Unmarshal(r.Auth.StorageJSON, &storage)
	if storage["extra"] != "kept" || storage["refresh_token"] != "browser-secret" || storage["api_key"] != "dashboard-secret" || storage["selected_team_id"] != float64(42) {
		t.Fatal("storage fields lost")
	}
	r, err = s.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{StorageJSON: parsed.Auth.StorageJSON, HTTPClient: client})
	if err != nil || r.Auth.ID != parsed.Auth.ID {
		t.Fatal("rotating access token changed ID")
	}
}

func TestBrowserRefreshRequiresReauthentication(t *testing.T) {
	s, _ := fixtureService(t)
	client := fakeHTTP{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		t.Fatal("browser refresh token was exchanged")
		return pluginapi.HTTPResponse{}, nil
	}}
	_, err := s.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{StorageJSON: []byte(`{"type":"cursor","access_token":"opaque","refresh_token":"browser-secret"}`), HTTPClient: client})
	if err == nil || !strings.Contains(err.Error(), "reauthentication") || strings.Contains(err.Error(), "browser-secret") {
		t.Fatalf("%v", err)
	}
	var status *HTTPStatusError
	if !errors.As(err, &status) || status.StatusCode != 401 {
		t.Fatal("reauthentication is not HTTP 401")
	}
}

func TestAPIKeyAuthExposesRefreshPolicyAndCurrentTokenMetadata(t *testing.T) {
	s, now := fixtureService(t)
	oldToken := fixtureJWT("account", now.Add(time.Hour))
	newToken := fixtureJWT("account", now.Add(2*time.Hour))
	raw, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": oldToken, "api_key": "dashboard-secret"})
	parsed, err := s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Auth.Attributes["refresh_interval_seconds"] != "300" || parsed.Auth.Metadata["access_token"] != oldToken || parsed.Auth.Metadata["expires_at"] != now.Add(time.Hour).UTC().Format(time.RFC3339) {
		t.Fatal("API-key auth omitted host refresh policy or token state")
	}
	client := fakeHTTP{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		body, _ := json.Marshal(map[string]string{"accessToken": newToken})
		return pluginapi.HTTPResponse{StatusCode: 200, Body: body}, nil
	}}
	refreshed, err := s.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{StorageJSON: parsed.Auth.StorageJSON, Metadata: map[string]any{"access_token": oldToken, "expires_at": "stale", "note": "kept"}, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Auth.Metadata["access_token"] != newToken || refreshed.Auth.Metadata["expires_at"] != now.Add(2*time.Hour).UTC().Format(time.RFC3339) || refreshed.Auth.Metadata["note"] != "kept" || refreshed.Auth.Attributes["refresh_interval_seconds"] != "300" {
		t.Fatal("refresh restored stale host metadata or lost its policy")
	}
}

func TestBrowserAuthDoesNotScheduleAPIKeyExchange(t *testing.T) {
	s, now := fixtureService(t)
	raw, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": fixtureJWT("account", now.Add(time.Hour)), "refresh_token": "browser-token"})
	parsed, err := s.ParseAuth(context.Background(), pluginapi.AuthParseRequest{RawJSON: raw})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Auth.Attributes["refresh_interval_seconds"] != "" {
		t.Fatal("browser auth received an API-key refresh schedule")
	}
}

func TestLoginPollFailuresAreRedacted(t *testing.T) {
	for _, body := range []string{`secret`, `{}`, `{"accessToken":"secret","refreshToken":17}`, `{"accessToken":"secret","refreshToken":"r","selectedTeamId":-1}`} {
		t.Run(body, func(t *testing.T) {
			s, _ := fixtureService(t)
			start, _ := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{})
			client := fakeHTTP{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
				return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(body)}, nil
			}}
			_, err := s.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: start.State, HTTPClient: client})
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestPollUsesVerifierQueryRequiredByCursor(t *testing.T) {
	s, _ := fixtureService(t)
	start, _ := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{})
	client := fakeHTTP{do: func(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		u, err := url.Parse(req.URL)
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("verifier") == "" || u.Query().Get("uuid") == "" {
			t.Fatal("Cursor poll identifiers missing")
		}
		return pluginapi.HTTPResponse{StatusCode: 404}, nil
	}}
	result, err := s.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: start.State, HTTPClient: client})
	if err != nil || result.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestLoginPollStopsAfterThreeFailures(t *testing.T) {
	s, now := fixtureService(t)
	start, _ := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{})
	client := fakeHTTP{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{StatusCode: 500}, nil
	}}
	for i := 0; i < 3; i++ {
		*now = now.Add(time.Second)
		result, err := s.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: start.State, HTTPClient: client})
		if i < 2 && err == nil {
			t.Fatalf("poll %d unexpectedly succeeded", i)
		}
		if i == 2 && (err != nil || result.Status != pluginapi.AuthLoginStatusError) {
			t.Fatalf("final result=%#v err=%v", result, err)
		}
	}
}

func TestConcurrentPollAndCloseCancellation(t *testing.T) {
	s, _ := fixtureService(t)
	start, _ := s.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{})
	entered := make(chan struct{})
	client := fakeHTTP{do: func(ctx context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		close(entered)
		<-ctx.Done()
		return pluginapi.HTTPResponse{}, ctx.Err()
	}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := s.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: start.State, HTTPClient: client})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%v", err)
		}
	}()
	<-entered
	r, err := s.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: start.State, HTTPClient: client})
	if err != nil || r.Status != pluginapi.AuthLoginStatusPending {
		t.Fatal("parallel poll not throttled")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}
