package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func quotaRequest(body string, status int) pluginapi.QuotaFetchRequest {
	return pluginapi.QuotaFetchRequest{Provider: "cursor", StorageJSON: []byte(`{"type":"cursor","access_token":"fixture-secret","selected_team_id":"123"}`), HTTPClient: fakeHTTP{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{StatusCode: status, Body: []byte(body)}, nil
	}}}
}
func TestQuotaOfficialFixture(t *testing.T) {
	q := NewQuotaService(DefaultConfig())
	req := quotaRequest(`{"billingCycleStart":"1791676800000","billingCycleEnd":"2026-11-11T00:00:00Z","planUsage":{"totalSpend":500,"includedSpend":400,"bonusSpend":100,"remaining":1500,"limit":2000,"totalPercentUsed":25,"autoPercentUsed":10,"apiPercentUsed":15},"spendLimitUsage":{"totalSpend":300,"individualLimit":1000,"individualUsed":300,"individualRemaining":700,"pooledLimit":"20000","pooledUsed":5000,"pooledRemaining":"15000","overallLimit":21000,"overallUsed":5300,"overallRemaining":15700,"limitType":"team"},"enabled":true,"serverTimeOffsetMs":123}`, 200)
	upstream := req.HTTPClient
	req.HTTPClient = fakeHTTP{do: func(ctx context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		if r.URL != "https://api2.cursor.sh"+quotaPath || r.Method != "POST" || r.Headers.Get("Authorization") != "Bearer fixture-secret" || r.Headers.Get("Content-Type") != "application/json" || r.Headers.Get("X-Cursor-Team-Id") != "123" || r.Headers.Get("X-Cursor-Client-Version") == "" {
			t.Fatalf("wrong quota request: %s", r.URL)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no deadline")
		}
		if string(r.Body) != `{"teamId":123,"includePooledUsage":true}` {
			t.Fatalf("body %s", r.Body)
		}
		return upstream.Do(ctx, r)
	}}
	r, err := q.FetchQuota(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Subscription != nil || len(r.Groups) != 2 || len(r.Groups[0].Buckets) != 3 || len(r.Groups[1].Buckets) != 3 || r.Groups[0].Buckets[0].RemainingFraction != .75 || r.Groups[1].Buckets[0].RemainingFraction != .75 || r.Groups[0].Buckets[0].ResetTime != "2026-11-11T00:00:00Z" || r.ServerTimeOffsetMs != 123 {
		t.Fatalf("wrong quota: %#v", r)
	}
	if r.Summary[3].Value != 5 || r.Summary[3].Currency != "USD" {
		t.Fatalf("cents conversion: %#v", r.Summary)
	}
}
func TestQuotaPartialNullAndZero(t *testing.T) {
	for _, tc := range []struct {
		body     string
		groups   int
		fraction float64
	}{
		{`{"planUsage":{"includedSpend":200,"limit":null}}`, 0, 0},
		{`{"planUsage":{"limit":1000,"remaining":250}}`, 1, .25},
		{`{"planUsage":{"totalPercentUsed":100}}`, 1, 0},
		{`{"planUsage":{"totalPercentUsed":0}}`, 1, 1},
		{`{"planUsage":{"limit":0,"remaining":0}}`, 1, 0},
		{`{"planUsage":{"totalPercentUsed":null,"includedSpend":0}}`, 0, 0},
		{`{"spendLimitUsage":{"overallLimit":null,"overallUsed":1200}}`, 0, 0},
	} {
		r, err := NewQuotaService(DefaultConfig()).FetchQuota(context.Background(), quotaRequest(tc.body, 200))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Groups) != tc.groups {
			t.Fatalf("%s: %#v", tc.body, r)
		}
		if tc.groups > 0 && r.Groups[0].Buckets[0].RemainingFraction != tc.fraction {
			t.Fatalf("%s: %#v", tc.body, r.Groups)
		}
	}
}
func TestQuotaRejectsMalformed(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `[]`, `{"enabled":true}`, `{"planUsage":{"totalPercentUsed":101}}`, `{"planUsage":{"totalPercentUsed":-1}}`, `{"planUsage":{"limit":-1}}`, `{"planUsage":{"includedSpend":"bad-secret"}}`, `{"planUsage":{"totalPercentUsed":1e999}}`, `{"billingCycleEnd":"bad-secret","planUsage":{"limit":0}}`, `{"planUsage":true}`, strings.Repeat(" ", maxJSONResponseBytes+1)} {
		_, err := NewQuotaService(DefaultConfig()).FetchQuota(context.Background(), quotaRequest(body, 200))
		if err == nil {
			t.Fatalf("accepted %s", body)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("leak")
		}
	}
}
func TestQuotaHTTPStatuses(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 503} {
		_, err := NewQuotaService(DefaultConfig()).FetchQuota(context.Background(), quotaRequest("secret", status))
		var typed *HTTPStatusError
		if !errors.As(err, &typed) || typed.StatusCode != status || strings.Contains(err.Error(), "secret") {
			t.Fatalf("%d: %v", status, err)
		}
	}
}
func TestQuotaCancellationAndMissingAuth(t *testing.T) {
	q := NewQuotaService(DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := q.FetchQuota(ctx, quotaRequest(`{}`, 200))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, storage := range []string{`{}`, `{"type":"other","access_token":"secret"}`, `{"type":"cursor","access_token":123}`, `{"type":"cursor","access_token":"secret","selected_team_id":"bad"}`, `{"type":"cursor","access_token":"secret","selected_team_id":1.5}`} {
		req := quotaRequest(`{}`, 200)
		req.StorageJSON = []byte(storage)
		req.HTTPClient = fakeHTTP{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
			t.Fatal("invalid auth reached network")
			return pluginapi.HTTPResponse{}, nil
		}}
		if _, err := q.FetchQuota(context.Background(), req); err == nil {
			t.Fatalf("accepted %s", storage)
		}
	}
	r, err := q.ResetQuota(context.Background(), pluginapi.QuotaResetRequest{})
	if err != nil || r.Success || r.Message == "" {
		t.Fatalf("reset %#v %v", r, err)
	}
	d, err := q.DescribeQuota(context.Background(), pluginapi.QuotaDescribeRequest{})
	if err != nil || d.DisplayName != "Cursor" || d.SupportsReset || len(d.SupportedProviders) != 1 {
		t.Fatalf("describe %#v", d)
	}
}
func TestCredentialValidationShared(t *testing.T) {
	for _, team := range []any{123.0, "123"} {
		raw, _ := json.Marshal(map[string]any{"type": "cursor", "access_token": "secret", "selected_team_id": team})
		m, err := credentialData(raw, nil)
		if err != nil || teamHeader(m["selected_team_id"]) != "123" {
			t.Fatalf("team %v %v", team, err)
		}
	}
	for _, raw := range []string{`{"type":"other","access_token":"secret"}`, `{"type":"cursor","access_token":""}`, `{"type":"cursor","access_token":1}`, `{"type":"cursor","access_token":"secret","selected_team_id":null}`} {
		if _, err := executorToken([]byte(raw), nil); err == nil {
			t.Fatal("executor accepted invalid credentials")
		}
		if _, err := NewExecutor(DefaultConfig()).headers([]byte(raw), nil, false, "id"); err == nil {
			t.Fatal("discovery accepted invalid credentials")
		}
	}
}

func TestConnectStatusMapping(t *testing.T) {
	for code, status := range map[string]int{"unauthenticated": 401, "permission_denied": 403, "resource_exhausted": 429, "unavailable": 503, "internal": 500} {
		var typed *HTTPStatusError
		if err := connectStatus(code); !errors.As(err, &typed) || typed.StatusCode != status {
			t.Fatalf("%s: %v", code, err)
		}
	}
	if strings.Contains(connectStatus("secret upstream message").Error(), "secret") {
		t.Fatal("Connect code leaked")
	}
}

func TestQuotaNoTeamAndDisabled(t *testing.T) {
	req := quotaRequest(`{"planUsage":{"totalPercentUsed":25}}`, 200)
	req.StorageJSON = []byte(`{"type":"cursor","access_token":"secret"}`)
	req.HTTPClient = fakeHTTP{do: func(ctx context.Context, r pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		if r.Headers.Get("X-Cursor-Team-Id") != "" || string(r.Body) != `{"includePooledUsage":true}` {
			t.Fatal("unexpected team")
		}
		return pluginapi.HTTPResponse{StatusCode: 200, Body: []byte(`{"planUsage":{"totalPercentUsed":25}}`)}, nil
	}}
	if _, err := NewQuotaService(DefaultConfig()).FetchQuota(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.HTTPClient = fakeHTTP{do: func(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
		t.Fatal("disabled quota reached upstream")
		return pluginapi.HTTPResponse{}, nil
	}}
	if _, err := NewQuotaService(Config{Enabled: false}).FetchQuota(context.Background(), req); err == nil {
		t.Fatal("disabled quota succeeded")
	}
}
