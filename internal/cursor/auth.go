// Authentication follows the official CLI cli-2026.10.01-e373342, distributed
// at https://downloads.cursor.com/lab/2026.10.01-e373342/linux/x64/agent-cli-package.tar.gz.
// The community reference inspected was
// https://github.com/Rahularya01/pi-cursor/blob/ef3cafb5c38081ce6e06df8e594241aabd9621e3/src/auth/oauth.ts.
// Its browser-refresh-token exchange is not confirmed by the official CLI and
// is intentionally not implemented here.
package cursor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const maxLoginSessions = 128
const pollInterval = time.Second

// ReauthenticationError identifies browser credentials that cannot be renewed
// using the official CLI protocol. Hosts may treat this as HTTP 401.
type ReauthenticationError struct{}

func (*ReauthenticationError) Error() string {
	return "cursor browser authentication requires reauthentication"
}
func (*ReauthenticationError) Unwrap() error {
	return &HTTPStatusError{StatusCode: http.StatusUnauthorized}
}

type loginSession struct {
	uuid, verifier    string
	expires, nextPoll time.Time
	inflight          bool
	failures          int
}

type AuthService struct {
	mu       sync.Mutex
	cfg      Config
	sessions map[string]*loginSession
	now      func() time.Time
	random   io.Reader
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
	wg       sync.WaitGroup
}

type authLifecycleKey struct{}

func lifecycleError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lifecycle, ok := ctx.Value(authLifecycleKey{}).(context.Context); ok {
		return lifecycle.Err()
	}
	return nil
}

var _ pluginapi.AuthProvider = (*AuthService)(nil)

func NewAuthService(cfg Config) *AuthService {
	ctx, cancel := context.WithCancel(context.Background())
	return &AuthService{cfg: cfg.WithDefaults(), sessions: make(map[string]*loginSession), now: time.Now, random: rand.Reader, ctx: ctx, cancel: cancel}
}

func (s *AuthService) Identifier() string { return "cursor" }

// SetConfig invalidates outstanding login state and cancels old upstream calls.
func (s *AuthService) SetConfig(cfg Config) error {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("cursor auth service is closed")
	}
	s.cancel()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.cfg = cfg
	clear(s.sessions)
	return nil
}

func (s *AuthService) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	clear(s.sessions)
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *AuthService) begin(ctx context.Context) (context.Context, Config, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.cfg.Enabled {
		return nil, Config{}, nil, errors.New("cursor auth service is unavailable")
	}
	if err := s.cfg.Validate(); err != nil {
		return nil, Config{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, Config{}, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	ctx = context.WithValue(ctx, authLifecycleKey{}, s.ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	s.wg.Add(1)
	return ctx, s.cfg, func() { stop(); cancel(); s.wg.Done() }, nil
}

// Storage retains unknown provider fields through refresh. JWT claims below
// are unverified hints for identity and scheduling, never proof of identity.
func parseStorage(raw []byte) (map[string]any, bool, error) {
	var m map[string]any
	if len(raw) > maxJSONResponseBytes || json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, false, errors.New("invalid cursor auth JSON")
	}
	if typ, _ := m["type"].(string); typ != "cursor" {
		return nil, false, nil
	}
	access, ok := m["access_token"].(string)
	if !ok || strings.TrimSpace(access) == "" || strings.ContainsAny(access, "\r\n") {
		return nil, true, errors.New("cursor auth requires an access token")
	}
	for _, key := range []string{"refresh_token", "api_key", "email", "id", "label", "prefix", "proxy_url", "file_name"} {
		if v, present := m[key]; present {
			if _, ok := v.(string); !ok {
				return nil, true, errors.New("invalid cursor auth field")
			}
		}
	}
	if v, ok := m["refresh_token"].(string); ok && strings.ContainsAny(v, "\r\n") {
		return nil, true, errors.New("invalid cursor refresh token")
	}
	if v, ok := m["api_key"].(string); ok && strings.ContainsAny(v, "\r\n") {
		return nil, true, errors.New("invalid cursor API key")
	}
	if v, present := m["selected_team_id"]; present {
		if teamHeader(v) == "" {
			return nil, true, errors.New("invalid cursor selected team")
		}
	}
	if v, present := m["disabled"]; present {
		if _, ok := v.(bool); !ok {
			return nil, true, errors.New("invalid cursor disabled field")
		}
	}
	if v, present := m["expires_at"]; present {
		if _, err := storageExpiry(v); err != nil {
			return nil, true, err
		}
	}
	for _, key := range []string{"metadata", "attributes"} {
		if v, present := m[key]; present {
			if _, ok := v.(map[string]any); !ok {
				return nil, true, errors.New("invalid cursor auth metadata")
			}
		}
	}
	return m, true, nil
}

// credentialData is shared by inference, discovery and quota. Persisted fields
// are authoritative: malformed storage cannot be repaired by metadata.
func credentialData(raw []byte, metadata map[string]any) (map[string]any, error) {
	if len(raw) == 0 {
		var err error
		raw, err = json.Marshal(metadata)
		if err != nil {
			return nil, errors.New("invalid cursor credentials")
		}
	}
	m, handled, err := parseStorage(raw)
	if err != nil {
		return nil, err
	}
	if !handled {
		return nil, errors.New("unsupported cursor credential type")
	}
	return m, nil
}

func storageExpiry(v any) (time.Time, error) {
	switch v := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, nil
		}
	case float64:
		if v > 0 && v < 253402300800 && v == float64(int64(v)) {
			return time.Unix(int64(v), 0), nil
		}
	}
	return time.Time{}, errors.New("invalid cursor token expiry")
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(parts[1]) > 16384 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	_ = json.Unmarshal(b, &claims)
	return claims
}

func tokenExpiry(token string, now time.Time) time.Time {
	if t, err := storageExpiry(jwtClaims(token)["exp"]); err == nil {
		return t
	}
	return now.Add(time.Hour)
}

func stringField(m map[string]any, key string) string { v, _ := m[key].(string); return v }

// sanitizeFileName keeps CPA-persisted auth file names inside the auth
// directory with a .json suffix the host scanner recognises.
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name = strings.Trim(b.String(), "._")
	if name == "" || len(name) > 128 {
		return ""
	}
	return name
}

func ensureJSONSuffix(name string) string {
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name += ".json"
	}
	return name
}

func authData(m map[string]any, now time.Time) (pluginapi.AuthData, error) {
	access := stringField(m, "access_token")
	claims := jwtClaims(access)
	id := stringField(m, "id")
	if id == "" {
		identity := stringField(claims, "sub")
		if identity == "" {
			identity = stringField(m, "api_key")
		}
		if identity == "" {
			identity = stringField(m, "refresh_token")
		}
		// Access-only opaque records have no stable account identity. Require a
		// caller-supplied ID rather than hashing a rotating access token.
		if identity == "" {
			return pluginapi.AuthData{}, errors.New("cursor auth requires a stable ID or refresh token")
		}
		hash := sha256.Sum256([]byte(identity))
		id = "cursor-" + hex.EncodeToString(hash[:16])
	}
	m["id"] = id
	// CPA only rescans *.json auth files on restart. An empty file name makes
	// the host persist the record under the bare auth ID, which its scanner
	// then skips and the credential is silently lost on reload.
	fileName := sanitizeFileName(stringField(m, "file_name"))
	if fileName == "" {
		fileName = sanitizeFileName(id)
	}
	if fileName == "" {
		return pluginapi.AuthData{}, errors.New("cursor auth requires a usable file name")
	}
	m["file_name"] = ensureJSONSuffix(fileName)
	if stringField(m, "email") == "" {
		if email := stringField(claims, "email"); email != "" {
			m["email"] = email
		}
	}
	expiry := tokenExpiry(access, now)
	if v, present := m["expires_at"]; present {
		expiry, _ = storageExpiry(v)
	} else {
		m["expires_at"] = expiry.UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return pluginapi.AuthData{}, errors.New("invalid cursor auth storage")
	}
	a := pluginapi.AuthData{Provider: "cursor", ID: id, FileName: stringField(m, "file_name"), Label: stringField(m, "label"), Prefix: stringField(m, "prefix"), ProxyURL: stringField(m, "proxy_url"), StorageJSON: raw, NextRefreshAfter: expiry.Add(-5 * time.Minute), Metadata: make(map[string]any), Attributes: make(map[string]string)}
	a.Disabled, _ = m["disabled"].(bool)
	if a.Label == "" {
		a.Label = stringField(m, "email")
	}
	if md, ok := m["metadata"].(map[string]any); ok {
		a.Metadata = maps.Clone(md)
	}
	if attrs, ok := m["attributes"].(map[string]any); ok {
		for k, v := range attrs {
			if text, ok := v.(string); ok {
				a.Attributes[k] = text
			}
		}
	}
	if email := stringField(m, "email"); email != "" {
		a.Metadata["email"] = email
	}
	a.Attributes["auth_kind"] = "oauth"
	a.Metadata["auth_kind"] = "oauth"
	return a, nil
}

func (s *AuthService) ParseAuth(ctx context.Context, req pluginapi.AuthParseRequest) (pluginapi.AuthParseResponse, error) {
	if req.Provider != "" && req.Provider != "cursor" {
		return pluginapi.AuthParseResponse{}, nil
	}
	m, handled, err := parseStorage(req.RawJSON)
	if err != nil || !handled {
		return pluginapi.AuthParseResponse{Handled: handled}, err
	}
	ctx, _, done, err := s.begin(ctx)
	if err != nil {
		return pluginapi.AuthParseResponse{Handled: true}, err
	}
	defer done()
	a, err := authData(m, s.now())
	if err != nil {
		return pluginapi.AuthParseResponse{Handled: true}, err
	}
	if name := sanitizeFileName(req.FileName); name != "" {
		a.FileName = ensureJSONSuffix(name)
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: a}, lifecycleError(ctx)
}

func (s *AuthService) StartLogin(ctx context.Context, req pluginapi.AuthLoginStartRequest) (pluginapi.AuthLoginStartResponse, error) {
	if req.Provider != "" && req.Provider != "cursor" {
		return pluginapi.AuthLoginStartResponse{}, errors.New("unsupported login provider")
	}
	ctx, cfg, done, err := s.begin(ctx)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	defer done()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := lifecycleError(ctx); err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	now := s.now()
	for state, session := range s.sessions {
		if !now.Before(session.expires) {
			delete(s.sessions, state)
		}
	}
	if len(s.sessions) >= maxLoginSessions {
		return pluginapi.AuthLoginStartResponse{}, errors.New("too many pending cursor logins")
	}
	b := make([]byte, 80)
	if _, err := io.ReadFull(s.random, b); err != nil {
		return pluginapi.AuthLoginStartResponse{}, errors.New("cursor login randomness unavailable")
	}
	verifier := base64.RawURLEncoding.EncodeToString(b[:32])
	state := base64.RawURLEncoding.EncodeToString(b[32:64])
	if _, exists := s.sessions[state]; exists {
		return pluginapi.AuthLoginStartResponse{}, errors.New("cursor login state collision")
	}
	// RFC 4122 random UUID, independent of the opaque host polling state.
	b[70] = (b[70] & 0x0f) | 0x40
	b[72] = (b[72] & 0x3f) | 0x80
	h := hex.EncodeToString(b[64:])
	uuid := h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	hash := sha256.Sum256([]byte(verifier))
	u, _ := url.Parse(cfg.LoginURL)
	u.RawQuery = url.Values{"challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "uuid": {uuid}, "mode": {"login"}, "redirectTarget": {"cli"}, "supportsSelectedTeamLogin": {"true"}}.Encode()
	expires := now.Add(time.Duration(cfg.LoginTimeout) * time.Second)
	s.sessions[state] = &loginSession{uuid: uuid, verifier: verifier, expires: expires}
	return pluginapi.AuthLoginStartResponse{Provider: "cursor", URL: u.String(), State: state, ExpiresAt: expires}, nil
}

type tokenResponse struct {
	AccessToken    string `json:"accessToken"`
	RefreshToken   string `json:"refreshToken"`
	SelectedTeamID *int64 `json:"selectedTeamId"`
}

func (s *AuthService) PollLogin(ctx context.Context, req pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	if req.Provider != "" && req.Provider != "cursor" {
		return pluginapi.AuthLoginPollResponse{}, errors.New("unsupported login provider")
	}
	ctx, cfg, done, err := s.begin(ctx)
	if err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	defer done()
	s.mu.Lock()
	session := s.sessions[req.State]
	now := s.now()
	if session == nil || !now.Before(session.expires) {
		delete(s.sessions, req.State)
		s.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "cursor login state is invalid or expired"}, nil
	}
	if session.inflight || now.Before(session.nextPoll) {
		s.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending}, nil
	}
	session.inflight = true
	session.nextPoll = now.Add(pollInterval)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); session.inflight = false; s.mu.Unlock() }()
	u, _ := url.Parse(strings.TrimRight(cfg.APIBaseURL, "/") + "/auth/poll")
	u.RawQuery = url.Values{"uuid": {session.uuid}, "verifier": {session.verifier}}.Encode()
	var tokens tokenResponse
	err = DoJSON(ctx, req.HTTPClient, pluginapi.HTTPRequest{Method: http.MethodGet, URL: u.String(), Headers: http.Header{"Content-Type": {"application/json"}}}, time.Duration(cfg.RequestTimeout)*time.Second, &tokens)
	var status *HTTPStatusError
	if errors.As(err, &status) && status.StatusCode == http.StatusNotFound {
		s.mu.Lock()
		session.failures = 0
		s.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending}, nil
	}
	if err != nil {
		s.mu.Lock()
		session.failures++
		terminal := session.failures >= 3 || (status != nil && status.StatusCode == http.StatusForbidden)
		if terminal {
			delete(s.sessions, req.State)
		}
		s.mu.Unlock()
		if terminal {
			return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "cursor login failed; start a new login"}, nil
		}
		return pluginapi.AuthLoginPollResponse{}, err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" || strings.TrimSpace(tokens.RefreshToken) == "" || strings.ContainsAny(tokens.AccessToken+tokens.RefreshToken, "\r\n") {
		s.mu.Lock()
		delete(s.sessions, req.State)
		s.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{}, errors.New("cursor login returned invalid tokens")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := lifecycleError(ctx); err != nil {
		return pluginapi.AuthLoginPollResponse{}, err
	}
	if s.sessions[req.State] != session || !s.now().Before(session.expires) {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "cursor login state is invalid or expired"}, nil
	}
	m := map[string]any{"type": "cursor", "access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken}
	if tokens.SelectedTeamID != nil {
		if *tokens.SelectedTeamID <= 0 || *tokens.SelectedTeamID > 2147483647 {
			return pluginapi.AuthLoginPollResponse{}, errors.New("cursor login returned invalid team")
		}
		m["selected_team_id"] = *tokens.SelectedTeamID
	}
	a, err := authData(m, s.now())
	if err == nil {
		delete(s.sessions, req.State)
	}
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Auth: a}, err
}

func (s *AuthService) RefreshAuth(ctx context.Context, req pluginapi.AuthRefreshRequest) (pluginapi.AuthRefreshResponse, error) {
	if req.AuthProvider != "" && req.AuthProvider != "cursor" {
		return pluginapi.AuthRefreshResponse{}, errors.New("unsupported refresh provider")
	}
	ctx, cfg, done, err := s.begin(ctx)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	defer done()
	m, handled, err := parseStorage(req.StorageJSON)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	if !handled {
		return pluginapi.AuthRefreshResponse{}, errors.New("unsupported cursor auth storage")
	}
	// The official CLI exchanges dashboard API keys, not browser refresh
	// tokens. Browser credentials require a new browser login on expiry.
	if strings.TrimSpace(stringField(m, "api_key")) == "" {
		return pluginapi.AuthRefreshResponse{}, &ReauthenticationError{}
	}
	// Establish identity before token rotation, including opaque refresh tokens.
	old, err := authData(m, s.now())
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	var tokens tokenResponse
	err = DoJSON(ctx, req.HTTPClient, pluginapi.HTTPRequest{Method: http.MethodPost, URL: strings.TrimRight(cfg.APIBaseURL, "/") + "/auth/exchange_user_api_key", Headers: http.Header{"Authorization": {"Bearer " + stringField(m, "api_key")}, "Content-Type": {"application/json"}}, Body: []byte("{}")}, time.Duration(cfg.RequestTimeout)*time.Second, &tokens)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" || strings.ContainsAny(tokens.AccessToken+tokens.RefreshToken, "\r\n") {
		return pluginapi.AuthRefreshResponse{}, errors.New("cursor refresh returned invalid tokens")
	}
	m["access_token"] = tokens.AccessToken
	if tokens.RefreshToken != "" {
		m["refresh_token"] = tokens.RefreshToken
	}
	m["expires_at"] = tokenExpiry(tokens.AccessToken, s.now()).UTC().Format(time.RFC3339)
	m["id"] = old.ID
	if req.AuthID != "" {
		m["id"] = req.AuthID
	}
	a, err := authData(m, s.now())
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	for k, v := range req.Metadata {
		a.Metadata[k] = v
	}
	for k, v := range req.Attributes {
		a.Attributes[k] = v
	}
	if err := lifecycleError(ctx); err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	return pluginapi.AuthRefreshResponse{Auth: a, NextRefreshAfter: a.NextRefreshAfter}, nil
}
