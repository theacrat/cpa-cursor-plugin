package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const quotaPath = "/aiserver.v1.DashboardService/GetCurrentPeriodUsage"

var _ pluginapi.QuotaProvider = (*QuotaService)(nil)

type QuotaService struct{ cfg Config }

func NewQuotaService(cfg Config) *QuotaService { return &QuotaService{cfg: cfg.WithDefaults()} }
func (q *QuotaService) Identifier() string     { return "cursor" }
func (q *QuotaService) DescribeQuota(context.Context, pluginapi.QuotaDescribeRequest) (pluginapi.QuotaDescribeResponse, error) {
	return pluginapi.QuotaDescribeResponse{SupportedProviders: []string{"cursor"}, DisplayName: "Cursor", SupportsReset: false}, nil
}
func (q *QuotaService) ResetQuota(context.Context, pluginapi.QuotaResetRequest) (pluginapi.QuotaResetResponse, error) {
	return pluginapi.QuotaResetResponse{Success: false, Message: "Cursor does not support quota reset"}, nil
}

type quotaNumber struct {
	Set   bool
	Null  bool
	Value float64
}

func (n *quotaNumber) UnmarshalJSON(raw []byte) error {
	n.Set = true
	if string(raw) == "null" {
		n.Null = true
		return nil
	}
	var v json.Number
	if err := json.Unmarshal(raw, &v); err != nil {
		return errors.New("invalid cursor quota number")
	}
	f, err := strconv.ParseFloat(string(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return errors.New("invalid cursor quota number")
	}
	n.Value = f
	return nil
}

type planUsage struct {
	TotalSpend       quotaNumber `json:"totalSpend"`
	IncludedSpend    quotaNumber `json:"includedSpend"`
	BonusSpend       quotaNumber `json:"bonusSpend"`
	Remaining        quotaNumber `json:"remaining"`
	Limit            quotaNumber `json:"limit"`
	AutoSpend        quotaNumber `json:"autoSpend"`
	APISpend         quotaNumber `json:"apiSpend"`
	AutoLimit        quotaNumber `json:"autoLimit"`
	APILimit         quotaNumber `json:"apiLimit"`
	AutoPercentUsed  quotaNumber `json:"autoPercentUsed"`
	APIPercentUsed   quotaNumber `json:"apiPercentUsed"`
	TotalPercentUsed quotaNumber `json:"totalPercentUsed"`
}
type spendLimitUsage struct {
	TotalSpend          quotaNumber `json:"totalSpend"`
	PooledLimit         quotaNumber `json:"pooledLimit"`
	PooledUsed          quotaNumber `json:"pooledUsed"`
	PooledRemaining     quotaNumber `json:"pooledRemaining"`
	IndividualLimit     quotaNumber `json:"individualLimit"`
	IndividualUsed      quotaNumber `json:"individualUsed"`
	IndividualRemaining quotaNumber `json:"individualRemaining"`
	LimitType           string      `json:"limitType"`
	OverallLimit        quotaNumber `json:"overallLimit"`
	OverallUsed         quotaNumber `json:"overallUsed"`
	OverallRemaining    quotaNumber `json:"overallRemaining"`
}
type usageResponse struct {
	BillingCycleStart  json.RawMessage  `json:"billingCycleStart"`
	BillingCycleEnd    json.RawMessage  `json:"billingCycleEnd"`
	MembershipType     string           `json:"membershipType"`
	PlanUsage          *planUsage       `json:"planUsage"`
	SpendLimitUsage    *spendLimitUsage `json:"spendLimitUsage"`
	Enabled            *bool            `json:"enabled"`
	DisplayMessage     string           `json:"displayMessage"`
	ServerTimeOffsetMs int64            `json:"serverTimeOffsetMs"`
}

func (q *QuotaService) FetchQuota(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
	cfg := q.cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	if !cfg.Enabled {
		return pluginapi.QuotaFetchResponse{}, errors.New("Cursor provider is disabled")
	}
	if req.Provider != "" && req.Provider != "cursor" {
		return pluginapi.QuotaFetchResponse{}, errors.New("unsupported cursor quota provider")
	}
	m, err := credentialData(req.StorageJSON, req.Metadata)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	target, err := endpoint(cfg.APIBaseURL, quotaPath)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	h := http.Header{"Authorization": {"Bearer " + stringField(m, "access_token")}, "Content-Type": {"application/json"}, "Connect-Protocol-Version": {"1"}, "X-Cursor-Client-Version": {cfg.ClientVersion}, "X-Cursor-Client-Type": {"cli"}, "X-Request-Id": {uuid()}}
	teamID := teamHeader(m["selected_team_id"])
	if teamID != "" {
		h.Set("X-Cursor-Team-Id", teamID)
	}
	requestBody := struct {
		TeamID             json.Number `json:"teamId,omitempty"`
		IncludePooledUsage bool        `json:"includePooledUsage"`
	}{IncludePooledUsage: true}
	if teamID != "" {
		requestBody.TeamID = json.Number(teamID)
	}
	var upstream usageResponse
	body, _ := json.Marshal(requestBody)
	if err := DoJSON(ctx, req.HTTPClient, pluginapi.HTTPRequest{Method: http.MethodPost, URL: target, Headers: h, Body: body}, time.Duration(cfg.RequestTimeout)*time.Second, &upstream); err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	return normaliseQuota(upstream)
}

func timestamp(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return millisTime(f)
		}
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return millisTime(n)
	}
	return ""
}
func millisTime(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v >= 253402300800000 || v != math.Trunc(v) {
		return ""
	}
	return time.UnixMilli(int64(v)).UTC().Format(time.RFC3339)
}
func validPercent(n quotaNumber) bool { return n.Set && !n.Null && n.Value >= 0 && n.Value <= 100 }
func validMoney(n quotaNumber) bool {
	return n.Set && !n.Null && n.Value >= 0 && !math.IsInf(n.Value, 0) && !math.IsNaN(n.Value)
}
func percentBucket(name string, pct quotaNumber, reset string) (pluginapi.QuotaBucket, bool) {
	if !validPercent(pct) {
		return pluginapi.QuotaBucket{}, false
	}
	return pluginapi.QuotaBucket{Window: name, RemainingFraction: (100 - pct.Value) / 100, ResetTime: reset, Description: "reported Cursor usage"}, true
}
func addMoney(out *[]pluginapi.QuotaMetric, key, label string, n quotaNumber) error {
	if !n.Set || n.Null {
		return nil
	}
	if !validMoney(n) {
		return errors.New("invalid cursor quota value")
	}
	*out = append(*out, pluginapi.QuotaMetric{Key: key, Label: label, Value: n.Value / 100, Unit: "USD", Format: "currency", Currency: "USD"})
	return nil
}
func amountBucket(name string, used, limit, remaining quotaNumber, reset string) (pluginapi.QuotaBucket, bool) {
	if !validMoney(limit) || (!validMoney(remaining) && !validMoney(used)) {
		return pluginapi.QuotaBucket{}, false
	}
	fraction := 0.0
	if limit.Value > 0 {
		if validMoney(remaining) {
			fraction = remaining.Value / limit.Value
		} else {
			fraction = 1 - used.Value/limit.Value
		}
	}
	fraction = math.Max(0, math.Min(1, fraction))
	return pluginapi.QuotaBucket{Window: name, RemainingFraction: fraction, ResetTime: reset}, true
}
func normaliseQuota(u usageResponse) (pluginapi.QuotaFetchResponse, error) {
	r := pluginapi.QuotaFetchResponse{ServerTimeOffsetMs: u.ServerTimeOffsetMs}
	for _, raw := range []json.RawMessage{u.BillingCycleStart, u.BillingCycleEnd} {
		if len(raw) > 0 && string(raw) != "null" && timestamp(raw) == "" {
			return r, errors.New("invalid cursor quota billing date")
		}
	}
	reset := timestamp(u.BillingCycleEnd)
	if u.PlanUsage != nil {
		g := pluginapi.QuotaGroup{DisplayName: "Plan"}
		p := u.PlanUsage
		for _, field := range []struct {
			key, label string
			n          quotaNumber
		}{{"total_percent_used", "Plan usage", p.TotalPercentUsed}, {"auto_percent_used", "Auto usage", p.AutoPercentUsed}, {"api_percent_used", "API usage", p.APIPercentUsed}} {
			if !field.n.Set || field.n.Null {
				continue
			}
			if !validPercent(field.n) {
				return r, errors.New("invalid cursor quota percentage")
			}
			r.Summary = append(r.Summary, pluginapi.QuotaMetric{Key: "plan." + field.key, Label: field.label, Value: field.n.Value, Unit: "percent", Format: "number"})
			b, _ := percentBucket(field.key, field.n, reset)
			g.Buckets = append(g.Buckets, b)
		}
		for _, field := range []struct {
			key, label string
			n          quotaNumber
		}{{"total_spend", "Plan spend", p.TotalSpend}, {"included_spend", "Included spend", p.IncludedSpend}, {"bonus_spend", "Bonus spend", p.BonusSpend}, {"remaining", "Plan remaining", p.Remaining}, {"limit", "Plan limit", p.Limit}, {"auto_spend", "Auto spend", p.AutoSpend}, {"api_spend", "API spend", p.APISpend}, {"auto_limit", "Auto limit", p.AutoLimit}, {"api_limit", "API limit", p.APILimit}} {
			if field.n.Set && !field.n.Null && !validMoney(field.n) {
				return r, errors.New("invalid cursor quota amount")
			}
			if field.key == "remaining" && p.Limit.Null {
				continue
			}
			if err := addMoney(&r.Summary, "plan."+field.key, field.label, field.n); err != nil {
				return r, err
			}
		}
		if !validPercent(p.TotalPercentUsed) {
			if b, ok := amountBucket("plan", p.TotalSpend, p.Limit, p.Remaining, reset); ok {
				g.Buckets = append(g.Buckets, b)
			}
		}
		for _, field := range []struct {
			name                 string
			percent, used, limit quotaNumber
		}{{"auto", p.AutoPercentUsed, p.AutoSpend, p.AutoLimit}, {"api", p.APIPercentUsed, p.APISpend, p.APILimit}} {
			if !validPercent(field.percent) {
				if b, ok := amountBucket(field.name, field.used, field.limit, quotaNumber{}, reset); ok {
					g.Buckets = append(g.Buckets, b)
				}
			}
		}
		if len(g.Buckets) > 0 {
			r.Groups = append(r.Groups, g)
		}
	}
	if u.SpendLimitUsage != nil {
		g := pluginapi.QuotaGroup{DisplayName: "On-demand"}
		s := u.SpendLimitUsage
		for _, field := range []struct {
			key, label string
			n          quotaNumber
		}{{"total_spend", "On-demand spend", s.TotalSpend}, {"pooled_limit", "Pooled limit", s.PooledLimit}, {"pooled_used", "Pooled spend", s.PooledUsed}, {"pooled_remaining", "Pooled remaining", s.PooledRemaining}, {"individual_limit", "Individual limit", s.IndividualLimit}, {"individual_used", "Individual spend", s.IndividualUsed}, {"individual_remaining", "Individual remaining", s.IndividualRemaining}, {"overall_limit", "Overall limit", s.OverallLimit}, {"overall_used", "Overall spend", s.OverallUsed}, {"overall_remaining", "Overall remaining", s.OverallRemaining}} {
			if field.n.Set && !field.n.Null && !validMoney(field.n) {
				return r, errors.New("invalid cursor quota amount")
			}
			if field.key == "pooled_remaining" && s.PooledLimit.Null || field.key == "individual_remaining" && s.IndividualLimit.Null || field.key == "overall_remaining" && s.OverallLimit.Null {
				continue
			}
			if err := addMoney(&r.Summary, "on_demand."+field.key, field.label, field.n); err != nil {
				return r, err
			}
		}
		for _, field := range []struct {
			name                   string
			used, limit, remaining quotaNumber
		}{{"pooled", s.PooledUsed, s.PooledLimit, s.PooledRemaining}, {"individual", s.IndividualUsed, s.IndividualLimit, s.IndividualRemaining}, {"overall", s.OverallUsed, s.OverallLimit, s.OverallRemaining}} {
			if b, ok := amountBucket(field.name, field.used, field.limit, field.remaining, reset); ok {
				if s.LimitType == "user" || s.LimitType == "team" {
					b.Description = s.LimitType
				}
				g.Buckets = append(g.Buckets, b)
			}
		}
		if len(g.Buckets) > 0 {
			r.Groups = append(r.Groups, g)
		}
	}
	if len(r.Summary) == 0 && len(r.Groups) == 0 && r.Subscription == nil && r.ServerTimeOffsetMs == 0 {
		return r, errors.New("cursor quota is unavailable")
	}
	return r, nil
}
