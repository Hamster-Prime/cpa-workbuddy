package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxRecentUsageEvents = 100

// TokenUsage captures normalized token counts for a single chat execution.
type TokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CachedTokens int64 `json:"cached_tokens,omitempty"`
	TotalTokens  int64 `json:"total_tokens"`
}

// UsageEvent records one completed chat execution for runtime usage statistics.
type UsageEvent struct {
	ID               string     `json:"id"`
	Timestamp        time.Time  `json:"timestamp"`
	AuthIndex        string     `json:"auth_index"`
	AccountLabel     string     `json:"account_label,omitempty"`
	Site             string     `json:"site"`
	Model            string     `json:"model"`
	Streaming        bool       `json:"streaming"`
	Status           string     `json:"status"` // success | error
	IsFree           bool       `json:"is_free,omitempty"`
	DurationMs       int64      `json:"duration_ms"`
	Usage            TokenUsage `json:"usage"`
	CreditFactor     float64    `json:"credit_factor,omitempty"`
	EstimatedCredits float64    `json:"estimated_credits,omitempty"`
	Error            string     `json:"error,omitempty"`
}

// ModelUsageSummary aggregates runtime usage and credit factor metadata per model.
type ModelUsageSummary struct {
	Model            string     `json:"model"`
	DisplayName      string     `json:"display_name,omitempty"`
	Site             string     `json:"site"`
	CreditFactor     float64    `json:"credit_factor"`
	IsFree           bool       `json:"is_free"`
	Requests         int64      `json:"requests"`
	SuccessCount     int64      `json:"success_count"`
	ErrorCount       int64      `json:"error_count"`
	InputTokens      int64      `json:"input_tokens"`
	OutputTokens     int64      `json:"output_tokens"`
	CachedTokens     int64      `json:"cached_tokens,omitempty"`
	TotalTokens      int64      `json:"total_tokens"`
	EstimatedCredits float64    `json:"estimated_credits"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
}

// AccountUsageSummary aggregates runtime usage alongside the latest quota snapshot for one account.
type AccountUsageSummary struct {
	AuthIndex        string         `json:"auth_index"`
	Label            string         `json:"label"`
	Site             string         `json:"site"`
	Scope            string         `json:"scope"`
	PlanName         string         `json:"plan_name,omitempty"`
	IsPaid           bool           `json:"is_paid"`
	Requests         int64          `json:"requests"`
	SuccessCount     int64          `json:"success_count" `
	ErrorCount       int64          `json:"error_count"`
	InputTokens      int64          `json:"input_tokens"`
	OutputTokens     int64          `json:"output_tokens"`
	TotalTokens      int64          `json:"total_tokens"`
	EstimatedCredits float64        `json:"estimated_credits"`
	LastUsedAt       *time.Time     `json:"last_used_at,omitempty"`
	Quota            *QuotaSnapshot `json:"quota,omitempty"`
}

// SiteCreditSummary aggregates credit balances and runtime usage per WorkBuddy site.
type SiteCreditSummary struct {
	Site             string  `json:"site"`
	AccountCount     int     `json:"account_count"`
	TotalCredits     float64 `json:"total_credits"`
	UsedCredits      float64 `json:"used_credits"`
	RemainingCredits float64 `json:"remaining_credits"`
	ExpiringCredits  float64 `json:"expiring_credits"`
	UsagePercent     float64 `json:"usage_percent"`
	Requests         int64   `json:"requests"`
	TotalTokens      int64   `json:"total_tokens"`
}

// UsageOverviewSummary provides top-level KPIs for the usage and credit statistics page.
type UsageOverviewSummary struct {
	TotalAccounts     int     `json:"total_accounts"`
	ActiveAccounts    int     `json:"active_accounts"`
	TotalCredits      float64 `json:"total_credits"`
	UsedCredits       float64 `json:"used_credits"`
	RemainingCredits  float64 `json:"remaining_credits"`
	ExpiringCredits   float64 `json:"expiring_credits"`
	UsagePercent      float64 `json:"usage_percent"`
	TotalRequests     int64   `json:"total_requests"`
	SuccessRequests   int64   `json:"success_requests"`
	ErrorRequests     int64   `json:"error_requests"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
	TotalTokens       int64   `json:"total_tokens"`
	EstimatedCredits  float64 `json:"estimated_credits"`
	ActiveModels      int     `json:"active_models"`
}

// UsageStatisticsResponse is returned by GET /v0/management/plugins/cpa-workbuddy/usage.
type UsageStatisticsResponse struct {
	GeneratedAt  time.Time             `json:"generated_at"`
	Overview     UsageOverviewSummary  `json:"overview"`
	Sites        []SiteCreditSummary   `json:"sites"`
	Accounts     []AccountUsageSummary `json:"accounts"`
	Models       []ModelUsageSummary   `json:"models"`
	RecentEvents []UsageEvent          `json:"recent_events"`
}

type usageTracker struct {
	mu      sync.RWMutex
	seq     atomic.Uint64
	models  map[string]*ModelUsageSummary
	account map[string]*AccountUsageSummary
	events  []UsageEvent
}

func newUsageTracker() *usageTracker {
	return &usageTracker{
		models:  make(map[string]*ModelUsageSummary),
		account: make(map[string]*AccountUsageSummary),
		events:  make([]UsageEvent, 0, maxRecentUsageEvents),
	}
}

func (u *usageTracker) reset() {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.models = make(map[string]*ModelUsageSummary)
	u.account = make(map[string]*AccountUsageSummary)
	u.events = u.events[:0]
}

func (u *usageTracker) record(ev UsageEvent) {
	if u == nil {
		return
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if ev.ID == "" {
		ev.ID = fmt.Sprintf("req-%d", u.seq.Add(1))
	}
	if ev.Usage.TotalTokens == 0 && (ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0) {
		ev.Usage.TotalTokens = ev.Usage.InputTokens + ev.Usage.OutputTokens
	}
	if ev.Status == "" {
		ev.Status = "success"
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	modelKey := ev.Site + "|" + ev.Model
	ms := u.models[modelKey]
	if ms == nil {
		ms = &ModelUsageSummary{
			Model:        ev.Model,
			Site:         ev.Site,
			CreditFactor: ev.CreditFactor,
			IsFree:       false,
		}
		u.models[modelKey] = ms
	}
	if ev.CreditFactor > 0 {
		ms.CreditFactor = ev.CreditFactor
		ms.IsFree = false
	}
	ms.Requests++
	if ev.Status == "success" {
		ms.SuccessCount++
	} else {
		ms.ErrorCount++
	}
	ms.InputTokens += ev.Usage.InputTokens
	ms.OutputTokens += ev.Usage.OutputTokens
	ms.CachedTokens += ev.Usage.CachedTokens
	ms.TotalTokens += ev.Usage.TotalTokens
	ms.EstimatedCredits += ev.EstimatedCredits
	ts := ev.Timestamp
	ms.LastUsedAt = &ts

	if ev.AuthIndex != "" {
		as := u.account[ev.AuthIndex]
		if as == nil {
			as = &AccountUsageSummary{
				AuthIndex: ev.AuthIndex,
				Label:     ev.AccountLabel,
				Site:      ev.Site,
			}
			u.account[ev.AuthIndex] = as
		}
		if ev.AccountLabel != "" {
			as.Label = ev.AccountLabel
		}
		as.Requests++
		if ev.Status == "success" {
			as.SuccessCount++
		} else {
			as.ErrorCount++
		}
		as.InputTokens += ev.Usage.InputTokens
		as.OutputTokens += ev.Usage.OutputTokens
		as.TotalTokens += ev.Usage.TotalTokens
		as.EstimatedCredits += ev.EstimatedCredits
		as.LastUsedAt = &ts
	}

	if len(u.events) >= maxRecentUsageEvents {
		copy(u.events, u.events[1:])
		u.events[len(u.events)-1] = ev
	} else {
		u.events = append(u.events, ev)
	}
}

func (u *usageTracker) snapshot() (models []ModelUsageSummary, accounts map[string]AccountUsageSummary, events []UsageEvent) {
	if u == nil {
		return nil, map[string]AccountUsageSummary{}, nil
	}
	u.mu.RLock()
	defer u.mu.RUnlock()

	models = make([]ModelUsageSummary, 0, len(u.models))
	for _, m := range u.models {
		models = append(models, *m)
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Requests != models[j].Requests {
			return models[i].Requests > models[j].Requests
		}
		if models[i].TotalTokens != models[j].TotalTokens {
			return models[i].TotalTokens > models[j].TotalTokens
		}
		return models[i].Model < models[j].Model
	})

	accounts = make(map[string]AccountUsageSummary, len(u.account))
	for k, v := range u.account {
		accounts[k] = *v
	}

	events = make([]UsageEvent, len(u.events))
	for i := range u.events {
		events[len(u.events)-1-i] = u.events[i]
	}
	return models, accounts, events
}

func (s *Service) buildUsageStatistics(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), refresh bool) (UsageStatisticsResponse, error) {
	dash, err := s.buildDashboard(ctx, callbackID, call, refresh, "")
	if err != nil {
		return UsageStatisticsResponse{}, err
	}

	trackedModels, trackedAccounts, recentEvents := s.usage.snapshot()
	catalogBySite := make(map[string]map[string]CatalogModel)

	siteMap := map[string]*SiteCreditSummary{
		"workbuddy":    {Site: "workbuddy"},
		"workbuddy-ai": {Site: "workbuddy-ai"},
	}

	var overview UsageOverviewSummary
	accounts := make([]AccountUsageSummary, 0, len(dash.Accounts))
	for _, acc := range dash.Accounts {
		overview.TotalAccounts++
		if !acc.Disabled && !acc.Unavailable {
			overview.ActiveAccounts++
		}

		summary := AccountUsageSummary{
			AuthIndex: acc.AuthIndex,
			Label:     acc.Label,
			Site:      acc.Site,
			Scope:     acc.Scope,
			Quota:     acc.Quota,
		}
		if acc.Quota != nil {
			summary.PlanName = acc.Quota.PlanName
			summary.IsPaid = acc.Quota.IsPaid
		}
		tracked, ok := trackedAccounts[acc.ID]
		if !ok {
			tracked, ok = trackedAccounts[acc.AuthIndex]
		}
		if ok {
			summary.Requests = tracked.Requests
			summary.SuccessCount = tracked.SuccessCount
			summary.ErrorCount = tracked.ErrorCount
			summary.InputTokens = tracked.InputTokens
			summary.OutputTokens = tracked.OutputTokens
			summary.TotalTokens = tracked.TotalTokens
			summary.EstimatedCredits = tracked.EstimatedCredits
			summary.LastUsedAt = tracked.LastUsedAt
		}
		accounts = append(accounts, summary)
		for i := range recentEvents {
			if recentEvents[i].AuthIndex == acc.ID || recentEvents[i].AuthIndex == acc.AuthIndex {
				if acc.Label != "" {
					recentEvents[i].AccountLabel = acc.Label
				}
			}
		}
		if rec, rErr := s.resolveAuthRecord(ctx, callbackID, call, acc.AuthIndex, nil); rErr == nil && rec.Stored.ModelSnapshot != nil {
			byModel := catalogBySite[acc.Site]
			if byModel == nil {
				byModel = make(map[string]CatalogModel)
				catalogBySite[acc.Site] = byModel
			}
			for _, cm := range rec.Stored.ModelSnapshot.Models {
				key := normalizeModelKey(cm.ID)
				if key != "" {
					byModel[key] = cm
				}
			}
		}

		sc := siteMap[acc.Site]
		if sc == nil {
			sc = &SiteCreditSummary{Site: acc.Site}
			siteMap[acc.Site] = sc
		}
		sc.AccountCount++
		sc.Requests += summary.Requests
		sc.TotalTokens += summary.TotalTokens

		if acc.Quota != nil {
			overview.TotalCredits += acc.Quota.Total
			overview.UsedCredits += acc.Quota.Used
			overview.RemainingCredits += acc.Quota.Remaining
			overview.ExpiringCredits += acc.Quota.ExpiringAmt

			sc.TotalCredits += acc.Quota.Total
			sc.UsedCredits += acc.Quota.Used
			sc.RemainingCredits += acc.Quota.Remaining
			sc.ExpiringCredits += acc.Quota.ExpiringAmt
		}
	}

	if overview.TotalCredits > 0 {
		overview.UsagePercent = math.Round((overview.UsedCredits/overview.TotalCredits)*1000) / 10
	}

	sites := make([]SiteCreditSummary, 0, len(siteMap))
	for _, siteID := range []string{"workbuddy", "workbuddy-ai"} {
		if sc := siteMap[siteID]; sc != nil {
			if sc.TotalCredits > 0 {
				sc.UsagePercent = math.Round((sc.UsedCredits/sc.TotalCredits)*1000) / 10
			}
			sites = append(sites, *sc)
		}
	}

	for i := range trackedModels {
		m := &trackedModels[i]
		if cm, ok := findCatalogModel(catalogBySite, m.Site, m.Model); ok {
			if cm.Name != "" {
				m.DisplayName = cm.Name
			}
			if cm.CreditRate != nil {
				m.CreditFactor = *cm.CreditRate
			}
			if cm.Free != nil && *cm.Free {
				m.IsFree = true
			}
		}
		if m.IsFree {
			m.EstimatedCredits = 0
		} else if m.CreditFactor > 0 {
			m.EstimatedCredits = math.Round(float64(m.SuccessCount)*m.CreditFactor*100) / 100
		}
		overview.TotalRequests += m.Requests
		overview.SuccessRequests += m.SuccessCount
		overview.ErrorRequests += m.ErrorCount
		overview.TotalInputTokens += m.InputTokens
		overview.TotalOutputTokens += m.OutputTokens
		overview.TotalTokens += m.TotalTokens
		overview.EstimatedCredits += m.EstimatedCredits
	}
	overview.EstimatedCredits = math.Round(overview.EstimatedCredits*100) / 100
	overview.ActiveModels = len(trackedModels)
	for i := range recentEvents {
		if cm, ok := findCatalogModel(catalogBySite, recentEvents[i].Site, recentEvents[i].Model); ok {
			if cm.CreditRate != nil {
				recentEvents[i].CreditFactor = *cm.CreditRate
			}
			if cm.Free != nil && *cm.Free {
				recentEvents[i].IsFree = true
			}
		}
		if recentEvents[i].Status == "success" && !recentEvents[i].IsFree && recentEvents[i].CreditFactor > 0 {
			recentEvents[i].EstimatedCredits = math.Round(recentEvents[i].CreditFactor*100) / 100
		}
	}

	return UsageStatisticsResponse{
		GeneratedAt:  time.Now().UTC(),
		Overview:     overview,
		Sites:        sites,
		Accounts:     accounts,
		Models:       trackedModels,
		RecentEvents: recentEvents,
	}, nil
}

func extractTokenUsageFromPayload(payload []byte) (TokenUsage, bool) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return TokenUsage{}, false
	}
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		var best TokenUsage
		var found bool
		for line := range bytes.SplitSeq(trimmed, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
				continue
			}
			if u, ok := extractTokenUsageFromJSON(data); ok {
				best = u
				found = true
			}
		}
		return best, found
	}
	return extractTokenUsageFromJSON(trimmed)
}

func extractTokenUsageFromJSON(data []byte) (TokenUsage, bool) {
	var root struct {
		Usage *struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			TotalTokens         int64 `json:"total_tokens"`
			InputTokens         int64 `json:"input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
			CacheReadInput      int64 `json:"cache_read_input_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Message *struct {
			Usage *struct {
				InputTokens    int64 `json:"input_tokens"`
				OutputTokens   int64 `json:"output_tokens"`
				CacheReadInput int64 `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return TokenUsage{}, false
	}
	if root.Usage != nil {
		in := root.Usage.PromptTokens
		if in == 0 {
			in = root.Usage.InputTokens
		}
		out := root.Usage.CompletionTokens
		if out == 0 {
			out = root.Usage.OutputTokens
		}
		cached := root.Usage.CacheReadInput
		if cached == 0 && root.Usage.PromptTokensDetails != nil {
			cached = root.Usage.PromptTokensDetails.CachedTokens
		}
		tot := root.Usage.TotalTokens
		if tot == 0 {
			tot = in + out
		}
		if in > 0 || out > 0 || tot > 0 || cached > 0 {
			return TokenUsage{
				InputTokens:  in,
				OutputTokens: out,
				CachedTokens: cached,
				TotalTokens:  tot,
			}, true
		}
	}
	if root.Message != nil && root.Message.Usage != nil {
		u := root.Message.Usage
		if u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheReadInput > 0 {
			return TokenUsage{
				InputTokens:  u.InputTokens,
				OutputTokens: u.OutputTokens,
				CachedTokens: u.CacheReadInput,
				TotalTokens:  u.InputTokens + u.OutputTokens,
			}, true
		}
	}
	return TokenUsage{}, false
}

func (s *Service) lookupModelCreditFactor(_, _ string) float64 {
	return 0
}

func normalizeModelKey(modelID string) string {
	m := strings.ToLower(strings.TrimSpace(modelID))
	m = strings.TrimPrefix(m, "wb-ai/")
	m = strings.TrimPrefix(m, "wb/")
	return m
}

func findCatalogModel(catalogBySite map[string]map[string]CatalogModel, siteID, modelID string) (CatalogModel, bool) {
	key := normalizeModelKey(modelID)
	if key == "" {
		return CatalogModel{}, false
	}
	if byModel := catalogBySite[siteID]; byModel != nil {
		if cm, ok := byModel[key]; ok {
			return cm, true
		}
	}
	for _, byModel := range catalogBySite {
		if cm, ok := byModel[key]; ok {
			return cm, true
		}
	}
	return CatalogModel{}, false
}
