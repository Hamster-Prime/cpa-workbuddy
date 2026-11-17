package workbuddy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cnCheckinStatusPath      = "/v2/billing/meter/checkin-activity-status"
	cnDailyCheckinPath       = "/v2/billing/meter/daily-checkin"
	intlHeatmapPath          = "/activity/growth/heatmap"
	intlReportPath           = "/v2/report"
	resourceSummaryPath      = "/billing/meter/get-user-resource-summary"
	resourcePaidPackagesPath = "/billing/meter/get-user-resource-paid-packages"
	resourceFreePackagesPath = "/billing/meter/get-user-resource-free-packages"
	enterpriseUsagePath      = "/billing/meter/get-enterprise-user-usage"
)

var (
	paidPackageCodes = []string{
		"TCACA_code_002_AkiJS3ZHF5",
		"TCACA_code_005_maRGyrHhw1",
		"TCACA_code_003_FAnt7lcmRT",
		"TCACA_code_023_4xbGhMrE6q",
		"TCACA_code_026_BaESVICNoi",
		"TCACA_code_027_0FCGVA6vSa",
		"TCACA_code_043_6oIkxT7Z0S",
		"TCACA_code_009_0XmEQc2xOf",
		"TCACA_code_038_OhvqZtiPKr",
		"TCACA_code_036_lupO5WgNdG",
	}
	freePackageCodes = []string{
		"TCACA_code_001_PqouKr6QWV",
		"TCACA_code_008_cfWoLwvjU4",
		"TCACA_code_035_ArVxJcGDsm",
		"TCACA_code_006_DbXS0lrypC",
		"TCACA_code_039_KRcQj7wUat",
		"TCACA_code_040_mi9rCYg46x",
		"TCACA_code_007_nzdH5h4Nl0",
		"TCACA_code_028_NtpWi0jzXs",
		"TCACA_code_037_WxOD3MpI2o",
		"TCACA_code_029_6wCGEWquYy",
		"TCACA_code_030_BjSt89qTvr",
	}
	planPackageCodes = map[string]bool{
		"TCACA_code_002_AkiJS3ZHF5": true,
		"TCACA_code_005_maRGyrHhw1": true,
		"TCACA_code_003_FAnt7lcmRT": true,
		"TCACA_code_023_4xbGhMrE6q": true,
		"TCACA_code_026_BaESVICNoi": true,
		"TCACA_code_027_0FCGVA6vSa": true,
		"TCACA_code_043_6oIkxT7Z0S": true,
		"TCACA_code_035_ArVxJcGDsm": true,
		"TCACA_code_039_KRcQj7wUat": true,
		"TCACA_code_040_mi9rCYg46x": true,
	}
	extraPackageCodes = map[string]bool{
		"TCACA_code_028_NtpWi0jzXs": true,
		"TCACA_code_037_WxOD3MpI2o": true,
		"TCACA_code_009_0XmEQc2xOf": true,
		"TCACA_code_038_OhvqZtiPKr": true,
		"TCACA_code_036_lupO5WgNdG": true,
		"TCACA_code_007_nzdH5h4Nl0": true,
		"TCACA_code_029_6wCGEWquYy": true,
		"TCACA_code_030_BjSt89qTvr": true,
	}
	extraAIPackageCodes = map[string]bool{
		"TCACA_code_008_cfWoLwvjU4": true,
		"TCACA_code_006_DbXS0lrypC": true,
	}
	freeMonPackageCode = "TCACA_code_008_cfWoLwvjU4"
	dailyPackageCode   = "TCACA_code_001_PqouKr6QWV"
)

var beijingLocation = time.FixedZone("Asia/Shanghai", 8*3600)

// QuotaPackageInfo describes one active credit bucket or package.
type QuotaPackageInfo struct {
	Code        string  `json:"code"`
	Name        string  `json:"name,omitempty"`
	Category    string  `json:"category"` // plan | extra | enterprise | unknown
	Total       float64 `json:"total"`
	Used        float64 `json:"used"`
	Remaining   float64 `json:"remaining"`
	ExpiresAt   string  `json:"expires_at,omitempty"`
	CycleEndsAt string  `json:"cycle_ends_at,omitempty"`
	IsSlice     bool    `json:"is_slice,omitempty"`
}

// QuotaSnapshot captures normalized account quota and credit balances.
type QuotaSnapshot struct {
	Site        string             `json:"site"`
	Scope       string             `json:"scope"` // personal | enterprise
	PlanCode    string             `json:"plan_code,omitempty"`
	PlanName    string             `json:"plan_name,omitempty"`
	IsPaid      bool               `json:"is_paid"`
	PlanTotal   float64            `json:"plan_total"`
	PlanUsed    float64            `json:"plan_used"`
	PlanRemain  float64            `json:"plan_remain"`
	ExtraTotal  float64            `json:"extra_total"`
	ExtraUsed   float64            `json:"extra_used"`
	ExtraRemain float64            `json:"extra_remain"`
	Total       float64            `json:"total"`
	Used        float64            `json:"used"`
	Remaining   float64            `json:"remaining"`
	ResetsAt    string             `json:"resets_at,omitempty"`
	ExpiringAt  string             `json:"expiring_at,omitempty"`
	ExpiringAmt float64            `json:"expiring_amount,omitempty"`
	Packages    []QuotaPackageInfo `json:"packages,omitempty"`
	FetchedAt   time.Time          `json:"fetched_at"`
}

// DailyActionResult captures the result of a CN check-in or Global daily growth bonus probe/trigger.
type DailyActionResult struct {
	AuthIndex   string    `json:"auth_index"`
	AuthID      string    `json:"auth_id"`
	Label       string    `json:"label,omitempty"`
	Site        string    `json:"site"`
	Kind        string    `json:"kind"`    // checkin | daily_bonus
	Outcome     string    `json:"outcome"` // claimed | done | lit | already_lit | inactive | ineligible | skipped | failed
	Credit      float64   `json:"credit,omitempty"`
	StreakDays  int       `json:"streak_days,omitempty"`
	TodayScore  int       `json:"today_score,omitempty"`
	TodayDate   string    `json:"today_date,omitempty"`
	Message     string    `json:"message,omitempty"`
	PerformedAt time.Time `json:"performed_at"`
}

// AccountBillingSnapshot caches the latest quota and daily action status for one credential.
type AccountBillingSnapshot struct {
	Quota       *QuotaSnapshot     `json:"quota,omitempty"`
	DailyStatus *DailyActionResult `json:"daily_status,omitempty"`
}

// QuotaDescribeResponse describes supported quota providers for CPA QuotaProvider RPC.
type QuotaDescribeResponse struct {
	SupportedProviders []string `json:"supported_providers,omitempty"`
	DisplayName        string   `json:"display_name,omitempty"`
	SupportsReset      bool     `json:"supports_reset,omitempty"`
}

type QuotaFetchRequest struct {
	AuthIndex      string            `json:"auth_index"`
	AuthID         string            `json:"auth_id"`
	Provider       string            `json:"provider"`
	StorageJSON    []byte            `json:"storage_json,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	HostCallbackID string            `json:"host_callback_id,omitempty"`
}

type QuotaSubscription struct {
	Plan     string `json:"plan,omitempty"`
	TierName string `json:"tierName,omitempty"`
	TierID   string `json:"tierId,omitempty"`
}

type QuotaMetric struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit,omitempty"`
	Format   string  `json:"format,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

type QuotaGroup struct {
	DisplayName string        `json:"displayName,omitempty"`
	Buckets     []QuotaBucket `json:"buckets,omitempty"`
}

type QuotaBucket struct {
	Window            string  `json:"window,omitempty"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
	Description       string  `json:"description,omitempty"`
}

type QuotaFetchResponse struct {
	Subscription       *QuotaSubscription `json:"subscription,omitempty"`
	Summary            []QuotaMetric      `json:"summary,omitempty"`
	ServerTimeOffsetMs int64              `json:"serverTimeOffsetMs,omitempty"`
	Groups             []QuotaGroup       `json:"groups,omitempty"`
}

type flexFloat float64

func (f *flexFloat) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte(`""`)) {
		*f = 0
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		val, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
			return fmt.Errorf("invalid numeric string %q", s)
		}
		*f = flexFloat(val)
		return nil
	}
	val, err := strconv.ParseFloat(string(trimmed), 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
		return fmt.Errorf("invalid numeric value %s", string(trimmed))
	}
	*f = flexFloat(val)
	return nil
}

type apiCallError struct {
	status       int
	code         int
	message      string
	invalidToken bool
	unreadable   bool
}

func (e *apiCallError) Error() string {
	if strings.TrimSpace(e.message) != "" {
		return e.message
	}
	return fmt.Sprintf("WorkBuddy API failed (status=%d code=%d)", e.status, e.code)
}

func (s *Service) accountLock(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acctLocks == nil {
		s.acctLocks = make(map[string]*sync.Mutex)
	}
	m := s.acctLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		s.acctLocks[key] = m
	}
	return m
}

func (s *Service) cacheQuota(authIndex string, snap QuotaSnapshot) {
	if authIndex == "" {
		return
	}
	s.billingMu.Lock()
	defer s.billingMu.Unlock()
	if s.billingCache == nil {
		s.billingCache = make(map[string]AccountBillingSnapshot)
	}
	cur := s.billingCache[authIndex]
	snapCopy := snap
	cur.Quota = &snapCopy
	s.billingCache[authIndex] = cur
}

func (s *Service) cacheDailyStatus(authIndex string, res DailyActionResult) {
	if authIndex == "" {
		return
	}
	s.billingMu.Lock()
	defer s.billingMu.Unlock()
	if s.billingCache == nil {
		s.billingCache = make(map[string]AccountBillingSnapshot)
	}
	cur := s.billingCache[authIndex]
	resCopy := res
	cur.DailyStatus = &resCopy
	s.billingCache[authIndex] = cur
}

func (s *Service) cachedBilling(authIndex string) AccountBillingSnapshot {
	s.billingMu.RLock()
	defer s.billingMu.RUnlock()
	if s.billingCache == nil {
		return AccountBillingSnapshot{}
	}
	cur := s.billingCache[authIndex]
	var out AccountBillingSnapshot
	if cur.Quota != nil {
		q := *cur.Quota
		out.Quota = &q
	}
	if cur.DailyStatus != nil {
		d := *cur.DailyStatus
		out.DailyStatus = &d
	}
	return out
}

func (s *Service) fetchQuotaRPC(ctx context.Context, raw []byte, call func(string, []byte) ([]byte, error)) (QuotaFetchResponse, error) {
	var req QuotaFetchRequest
	if err := decode(raw, &req); err != nil {
		return QuotaFetchResponse{}, err
	}
	if strings.TrimSpace(req.AuthIndex) == "" && len(bytes.TrimSpace(req.StorageJSON)) == 0 {
		return QuotaFetchResponse{}, failure("invalid_request", "auth_index or storage_json is required", 400)
	}
	call = s.bindHostCall(call)
	snap, err := s.FetchAccountQuota(ctx, req.HostCallbackID, call, req.AuthIndex, req.StorageJSON)
	if err != nil {
		return QuotaFetchResponse{}, err
	}
	return quotaSnapshotToRPC(snap), nil
}

// FetchAccountQuota queries WorkBuddy quota and package balances for a credential.
func (s *Service) FetchAccountQuota(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), authIndex string, inlineStorage []byte) (QuotaSnapshot, error) {
	rec, err := s.resolveAuthRecord(ctx, callbackID, call, authIndex, inlineStorage)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	lockKey := rec.AuthIndex
	if lockKey == "" {
		lockKey = rec.Auth.ID
	}
	mu := s.accountLock(lockKey)
	mu.Lock()
	defer mu.Unlock()

	snap, nextRec, err := s.fetchQuotaWithRefresh(ctx, callbackID, call, rec)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	s.cacheQuota(nextRec.AuthIndex, snap)
	return snap, nil
}

func (s *Service) fetchQuotaWithRefresh(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord) (QuotaSnapshot, hostAuthRecord, error) {
	snap, err := fetchQuotaForRecord(ctx, callbackID, call, rec, time.Now())
	if err != nil && isAPIUnauthorized(err) {
		if refreshed, rErr := s.refreshAndSaveRecord(ctx, callbackID, call, rec); rErr == nil {
			rec = refreshed
			snap, err = fetchQuotaForRecord(ctx, callbackID, call, rec, time.Now())
		}
	}
	if err != nil {
		return QuotaSnapshot{}, rec, toPublicError(err)
	}
	return snap, rec, nil
}

type summaryPackageDTO struct {
	PackageCode         string    `json:"PackageCode"`
	PackageName         string    `json:"PackageName"`
	CycleTotalCapacity  flexFloat `json:"CycleTotalCapacity"`
	CycleUsedCapacity   flexFloat `json:"CycleUsedCapacity"`
	CycleRemainCapacity flexFloat `json:"CycleRemainCapacity"`
}

type creditGroupDTO struct {
	Kind                string    `json:"Kind"`
	CycleTotalCapacity  flexFloat `json:"CycleTotalCapacity"`
	CycleUsedCapacity   flexFloat `json:"CycleUsedCapacity"`
	CycleRemainCapacity flexFloat `json:"CycleRemainCapacity"`
}

type resourceSummaryDTO struct {
	IsPaidUser              bool                `json:"IsPaidUser"`
	SubscriptionPackageCode string              `json:"SubscriptionPackageCode"`
	SubscriptionPackageName string              `json:"SubscriptionPackageName"`
	Packages                []summaryPackageDTO `json:"Packages"`
	CreditGroups            []creditGroupDTO    `json:"CreditGroups"`
}

type slicePeriodDetailDTO struct {
	SlicePeriodCapacitySizePrecise   flexFloat `json:"SlicePeriodCapacitySizePrecise"`
	SlicePeriodCapacityRemainPrecise flexFloat `json:"SlicePeriodCapacityRemainPrecise"`
}

type resourcePackageDTO struct {
	PackageCode              string                 `json:"PackageCode"`
	PackageName              string                 `json:"PackageName"`
	CapacityType             json.RawMessage        `json:"CapacityType"`
	DeductionEndTime         json.RawMessage        `json:"DeductionEndTime"`
	ExpiredTime              string                 `json:"ExpiredTime"`
	CycleStartTime           string                 `json:"CycleStartTime"`
	CycleEndTime             string                 `json:"CycleEndTime"`
	CycleCapacitySizePrecise flexFloat              `json:"CycleCapacitySizePrecise"`
	CycleCapacityRemain      flexFloat              `json:"CycleCapacityRemain"`
	CycleCapacityRemainPrec  flexFloat              `json:"CycleCapacityRemainPrecise"`
	SlicePeriodUsageDetails  []slicePeriodDetailDTO `json:"SlicePeriodUsageDetails"`
}

type enterpriseUsageDTO struct {
	LimitNum       flexFloat `json:"limitNum"`
	Credit         flexFloat `json:"credit"`
	CycleStartTime string    `json:"cycleStartTime"`
	CycleResetTime string    `json:"cycleResetTime"`
}

func fetchQuotaForRecord(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord, now time.Time) (QuotaSnapshot, error) {
	scope := "personal"
	if rec.Stored.EnterpriseID != "" {
		scope = "enterprise"
	}
	var summary resourceSummaryDTO
	if _, err := callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, resourceSummaryPath, []byte("{}"), &summary); err != nil {
		return QuotaSnapshot{}, err
	}

	if scope == "enterprise" && len(summary.Packages) == 0 && len(summary.CreditGroups) == 0 {
		var ent enterpriseUsageDTO
		if _, err := callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, enterpriseUsagePath, []byte("{}"), &ent); err != nil {
			return QuotaSnapshot{}, err
		}
		return buildEnterpriseQuotaSnapshot(rec, ent, now), nil
	}

	packages, pkgErr := fetchDetailPackages(ctx, rec, callbackID, call, now)
	if pkgErr != nil && isAPIUnauthorized(pkgErr) {
		return QuotaSnapshot{}, pkgErr
	}
	return buildPersonalQuotaSnapshot(rec, summary, packages, now), nil
}

func fetchDetailPackages(ctx context.Context, rec hostAuthRecord, callbackID string, call func(string, []byte) ([]byte, error), now time.Time) ([]resourcePackageDTO, error) {
	day := now.In(beijingLocation).Format("2006-01-02")
	queries := []struct {
		path string
		body func(page int) map[string]any
	}{
		{
			path: resourcePaidPackagesPath,
			body: func(page int) map[string]any {
				return map[string]any{
					"PackageCodes":  paidPackageCodes,
					"NeedRenewInfo": true,
					"PageNumber":    page,
					"PageSize":      200,
					"Status":        []int{0, 3},
				}
			},
		},
		{
			path: resourceFreePackagesPath,
			body: func(page int) map[string]any {
				return map[string]any{
					"PackageCodes":         freePackageCodes,
					"SlicePeriodStartTime": day + " 00:00:00",
					"SlicePeriodEndTime":   day + " 23:59:59",
					"PageNumber":           page,
					"PageSize":             200,
					"Status":               []int{0, 3},
				}
			},
		},
	}

	var out []resourcePackageDTO
	for _, q := range queries {
		for page := 1; page <= 10; page++ {
			payload, err := json.Marshal(q.body(page))
			if err != nil {
				return out, err
			}
			var pageResp struct {
				Accounts   []resourcePackageDTO `json:"Accounts"`
				TotalCount int                  `json:"TotalCount"`
			}
			if _, err := callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, q.path, payload, &pageResp); err != nil {
				return out, err
			}
			out = append(out, pageResp.Accounts...)
			if len(pageResp.Accounts) < 200 || (pageResp.TotalCount > 0 && len(out) >= pageResp.TotalCount) {
				break
			}
		}
	}
	return out, nil
}

func buildEnterpriseQuotaSnapshot(rec hostAuthRecord, ent enterpriseUsageDTO, now time.Time) QuotaSnapshot {
	total := float64(ent.LimitNum)
	used := float64(ent.Credit)
	remain := math.Max(0, total-used)
	var resetsAt string
	if t, ok := parseBeijingTime(ent.CycleResetTime); ok {
		resetsAt = t.UTC().Format(time.RFC3339)
	}
	snap := QuotaSnapshot{
		Site:       rec.Site.id,
		Scope:      "enterprise",
		PlanName:   "Enterprise",
		IsPaid:     true,
		PlanTotal:  total,
		PlanUsed:   used,
		PlanRemain: remain,
		Total:      total,
		Used:       used,
		Remaining:  remain,
		ResetsAt:   resetsAt,
		FetchedAt:  now.UTC(),
	}
	if total > 0 {
		snap.Packages = []QuotaPackageInfo{{
			Code:        "enterprise",
			Name:        "Enterprise Credits",
			Category:    "enterprise",
			Total:       total,
			Used:        used,
			Remaining:   remain,
			CycleEndsAt: resetsAt,
		}}
	}
	return snap
}

func buildPersonalQuotaSnapshot(rec hostAuthRecord, sum resourceSummaryDTO, packages []resourcePackageDTO, now time.Time) QuotaSnapshot {
	isIntl := rec.Site.id == "workbuddy-ai"
	isPlanCode := func(code string) bool {
		if code == "" {
			return false
		}
		if code == sum.SubscriptionPackageCode || planPackageCodes[code] {
			return true
		}
		return !isIntl && code == freeMonPackageCode
	}
	isExtraCode := func(code string) bool {
		if extraPackageCodes[code] {
			return true
		}
		return isIntl && extraAIPackageCodes[code]
	}

	var planTotal, planUsed, extraTotal, extraUsed float64
	hasGroups := false
	for _, g := range sum.CreditGroups {
		if g.CycleTotalCapacity <= 0 && g.CycleUsedCapacity <= 0 && g.CycleRemainCapacity <= 0 {
			continue
		}
		hasGroups = true
		if g.Kind == "plan_base" {
			planTotal += float64(g.CycleTotalCapacity)
			planUsed += float64(g.CycleUsedCapacity)
		} else {
			extraTotal += float64(g.CycleTotalCapacity)
			extraUsed += float64(g.CycleUsedCapacity)
		}
	}

	summedCodes := make(map[string]bool, len(sum.Packages))
	var prepaidRemain float64
	for _, p := range sum.Packages {
		if p.PackageCode != "" {
			summedCodes[p.PackageCode] = true
		}
		if p.CycleTotalCapacity > 0 {
			if !hasGroups {
				if isPlanCode(p.PackageCode) {
					planTotal += float64(p.CycleTotalCapacity)
					planUsed += float64(p.CycleUsedCapacity)
				} else {
					extraTotal += float64(p.CycleTotalCapacity)
					extraUsed += float64(p.CycleUsedCapacity)
				}
			}
		} else if p.CycleRemainCapacity > 0 {
			prepaidRemain += float64(p.CycleRemainCapacity)
		}
	}

	var packageInfos []QuotaPackageInfo
	var soonestExpiry time.Time
	var expiringAmt float64
	var resetsAt string

	for _, p := range packages {
		isSlice := strings.Trim(string(bytes.TrimSpace(p.CapacityType)), `"`) == "4"
		endTime := packageEndTime(p)
		if !endTime.IsZero() && !endTime.After(now) {
			continue
		}

		var pTotal, pRemain float64
		if isSlice && len(p.SlicePeriodUsageDetails) > 0 {
			pTotal = float64(p.SlicePeriodUsageDetails[0].SlicePeriodCapacitySizePrecise)
			pRemain = math.Min(pTotal, float64(p.SlicePeriodUsageDetails[0].SlicePeriodCapacityRemainPrecise))
		} else {
			pTotal = float64(p.CycleCapacitySizePrecise)
			if p.CycleCapacityRemainPrec > 0 {
				pRemain = float64(p.CycleCapacityRemainPrec)
			} else {
				pRemain = float64(p.CycleCapacityRemain)
			}
			if pTotal > 0 {
				pRemain = math.Min(pTotal, pRemain)
			}
		}

		if isExtraCode(p.PackageCode) && !summedCodes[p.PackageCode] && pTotal > 0 {
			extraTotal += pTotal
			extraUsed += math.Max(0, pTotal-pRemain)
		}

		if pRemain > 0 && !endTime.IsZero() && !isSlice && p.PackageCode != dailyPackageCode && (summedCodes[p.PackageCode] || isExtraCode(p.PackageCode)) {
			if soonestExpiry.IsZero() || endTime.Before(soonestExpiry) {
				soonestExpiry = endTime
				expiringAmt = pRemain
			} else if endTime.Equal(soonestExpiry) {
				expiringAmt += pRemain
			}
		}

		if sum.SubscriptionPackageCode != "" && p.PackageCode == sum.SubscriptionPackageCode {
			if cycleEnd, ok := parseBeijingTime(p.CycleEndTime); ok && cycleEnd.After(now) {
				resetsAt = cycleEnd.UTC().Format(time.RFC3339)
			}
		} else if sum.SubscriptionPackageCode == "" && resetsAt == "" && isSlice {
			if cycleEnd, ok := parseBeijingTime(p.CycleEndTime); ok && cycleEnd.After(now) {
				resetsAt = cycleEnd.UTC().Format(time.RFC3339)
			}
		}

		if pTotal > 0 || pRemain > 0 {
			cat := "unknown"
			switch {
			case isPlanCode(p.PackageCode):
				cat = "plan"
			case isExtraCode(p.PackageCode):
				cat = "extra"
			}
			info := QuotaPackageInfo{
				Code:      p.PackageCode,
				Name:      p.PackageName,
				Category:  cat,
				Total:     pTotal,
				Used:      math.Max(0, pTotal-pRemain),
				Remaining: pRemain,
				IsSlice:   isSlice,
			}
			if !endTime.IsZero() {
				info.ExpiresAt = endTime.UTC().Format(time.RFC3339)
			}
			if cycleEnd, ok := parseBeijingTime(p.CycleEndTime); ok {
				info.CycleEndsAt = cycleEnd.UTC().Format(time.RFC3339)
			}
			packageInfos = append(packageInfos, info)
		}
	}

	if prepaidRemain > 0 {
		extraTotal += prepaidRemain
	}

	planRemain := math.Max(0, planTotal-planUsed)
	extraRemain := math.Max(0, extraTotal-extraUsed)
	total := planTotal + extraTotal
	used := planUsed + extraUsed
	remain := math.Max(0, total-used)

	planName := strings.TrimSpace(sum.SubscriptionPackageName)
	if planName == "" {
		if sum.IsPaidUser {
			planName = "Paid"
		} else {
			planName = "Free"
		}
	}
	var expiringAt string
	if !soonestExpiry.IsZero() {
		expiringAt = soonestExpiry.UTC().Format(time.RFC3339)
	}

	return QuotaSnapshot{
		Site:        rec.Site.id,
		Scope:       "personal",
		PlanCode:    sum.SubscriptionPackageCode,
		PlanName:    planName,
		IsPaid:      sum.IsPaidUser,
		PlanTotal:   planTotal,
		PlanUsed:    planUsed,
		PlanRemain:  planRemain,
		ExtraTotal:  extraTotal,
		ExtraUsed:   extraUsed,
		ExtraRemain: extraRemain,
		Total:       total,
		Used:        used,
		Remaining:   remain,
		ResetsAt:    resetsAt,
		ExpiringAt:  expiringAt,
		ExpiringAmt: expiringAmt,
		Packages:    packageInfos,
		FetchedAt:   now.UTC(),
	}
}

func quotaSnapshotToRPC(snap QuotaSnapshot) QuotaFetchResponse {
	tierName := "Free"
	if snap.IsPaid {
		tierName = "Pro"
	}
	if snap.Scope == "enterprise" {
		tierName = "Enterprise"
	}
	resp := QuotaFetchResponse{
		Subscription: &QuotaSubscription{
			Plan:     snap.PlanName,
			TierName: tierName,
			TierID:   snap.PlanCode,
		},
		Summary: []QuotaMetric{
			{Key: "remaining_credits", Label: "Remaining Credits", Value: snap.Remaining, Unit: "credits", Format: "number"},
			{Key: "total_credits", Label: "Total Credits", Value: snap.Total, Unit: "credits", Format: "number"},
			{Key: "used_credits", Label: "Used Credits", Value: snap.Used, Unit: "credits", Format: "number"},
			{Key: "extra_remaining_credits", Label: "Bonus & Check-in Credits", Value: snap.ExtraRemain, Unit: "credits", Format: "number"},
		},
	}

	var buckets []QuotaBucket
	if snap.Total > 0 {
		buckets = append(buckets, QuotaBucket{
			Window:            "Total Credits",
			RemainingFraction: clampFraction(snap.Remaining / snap.Total),
			ResetTime:         snap.ResetsAt,
			Description:       fmt.Sprintf("%.2f / %.2f credits remaining", snap.Remaining, snap.Total),
		})
	}
	if snap.PlanTotal > 0 {
		buckets = append(buckets, QuotaBucket{
			Window:            "Plan Base",
			RemainingFraction: clampFraction(snap.PlanRemain / snap.PlanTotal),
			ResetTime:         snap.ResetsAt,
			Description:       fmt.Sprintf("%.2f / %.2f plan credits remaining", snap.PlanRemain, snap.PlanTotal),
		})
	}
	if snap.ExtraTotal > 0 {
		buckets = append(buckets, QuotaBucket{
			Window:            "Bonus / Check-in",
			RemainingFraction: clampFraction(snap.ExtraRemain / snap.ExtraTotal),
			ResetTime:         snap.ExpiringAt,
			Description:       fmt.Sprintf("%.2f / %.2f extra credits remaining", snap.ExtraRemain, snap.ExtraTotal),
		})
	}
	if len(buckets) > 0 {
		resp.Groups = []QuotaGroup{{DisplayName: "WorkBuddy Credits", Buckets: buckets}}
	}
	return resp
}

func clampFraction(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ProbeAccountDailyStatus inspects today's check-in (CN) or growth heatmap (Global) without mutating.
func (s *Service) ProbeAccountDailyStatus(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord) (DailyActionResult, hostAuthRecord, error) {
	if rec.Site.id == "workbuddy" {
		res, nextRec, err := s.probeCNCheckin(ctx, callbackID, call, rec)
		if err == nil {
			s.cacheDailyStatus(nextRec.AuthIndex, res)
		}
		return res, nextRec, err
	}
	res, nextRec, err := s.probeGlobalHeatmap(ctx, callbackID, call, rec)
	if err == nil {
		s.cacheDailyStatus(nextRec.AuthIndex, res)
	}
	return res, nextRec, err
}

// RunAccountCheckin executes CN daily check-in (100 credits) for a domestic WorkBuddy credential.
func (s *Service) RunAccountCheckin(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), authIndex string) (DailyActionResult, error) {
	rec, err := s.resolveAuthRecord(ctx, callbackID, call, authIndex, nil)
	if err != nil {
		return DailyActionResult{}, err
	}
	return s.runAccountCheckinRecord(ctx, callbackID, call, rec), nil
}

func (s *Service) runAccountCheckinRecord(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord) DailyActionResult {
	mu := s.accountLock(rec.AuthIndex)
	mu.Lock()
	defer mu.Unlock()

	if rec.Site.id != "workbuddy" {
		return DailyActionResult{
			AuthIndex:   rec.AuthIndex,
			AuthID:      rec.Auth.ID,
			Label:       rec.Auth.Label,
			Site:        rec.Site.id,
			Kind:        "checkin",
			Outcome:     "skipped",
			Message:     "国内每日签到仅适用于 WorkBuddy 国内版账号",
			PerformedAt: time.Now().UTC(),
		}
	}

	res, nextRec := s.executeCNCheckin(ctx, callbackID, call, rec)
	s.cacheDailyStatus(nextRec.AuthIndex, res)
	if res.Outcome == "claimed" || res.Outcome == "done" {
		if snap, _, qErr := s.fetchQuotaWithRefresh(ctx, callbackID, call, nextRec); qErr == nil {
			s.cacheQuota(nextRec.AuthIndex, snap)
		}
	}
	return res
}

// RunAccountDailyBonus executes Global growth center daily bonus (30/50 credits) via heatmap probe + /v2/report.
func (s *Service) RunAccountDailyBonus(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), authIndex string) (DailyActionResult, error) {
	rec, err := s.resolveAuthRecord(ctx, callbackID, call, authIndex, nil)
	if err != nil {
		return DailyActionResult{}, err
	}
	return s.runAccountDailyBonusRecord(ctx, callbackID, call, rec), nil
}

func (s *Service) runAccountDailyBonusRecord(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord) DailyActionResult {
	mu := s.accountLock(rec.AuthIndex)
	mu.Lock()
	defer mu.Unlock()

	if rec.Site.id != "workbuddy-ai" {
		return DailyActionResult{
			AuthIndex:   rec.AuthIndex,
			AuthID:      rec.Auth.ID,
			Label:       rec.Auth.Label,
			Site:        rec.Site.id,
			Kind:        "daily_bonus",
			Outcome:     "skipped",
			Message:     "国际成长中心每日点亮仅适用于 WorkBuddy AI 国际版账号",
			PerformedAt: time.Now().UTC(),
		}
	}

	res, nextRec := s.executeGlobalDailyBonus(ctx, callbackID, call, rec)
	s.cacheDailyStatus(nextRec.AuthIndex, res)
	return res
}

type cnCheckinStatusDTO struct {
	Active           bool      `json:"active"`
	TodayCheckedIn   bool      `json:"today_checked_in"`
	TodayCheckedInCC bool      `json:"todayCheckedIn"`
	TodayCredit      flexFloat `json:"today_credit"`
	DailyCredit      flexFloat `json:"daily_credit"`
	StreakDays       int       `json:"streak_days"`
}

type cnCheckinResultDTO struct {
	Credit     flexFloat `json:"credit"`
	StreakDays int       `json:"streak_days"`
	Message    string    `json:"message"`
}

func (s *Service) probeCNCheckin(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord) (DailyActionResult, hostAuthRecord, error) {
	now := time.Now().UTC()
	res := DailyActionResult{
		AuthIndex:   rec.AuthIndex,
		AuthID:      rec.Auth.ID,
		Label:       rec.Auth.Label,
		Site:        rec.Site.id,
		Kind:        "checkin",
		PerformedAt: now,
	}
	var st cnCheckinStatusDTO
	_, err := callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, cnCheckinStatusPath, []byte("{}"), &st)
	if err != nil && isAPIUnauthorized(err) {
		if refreshed, rErr := s.refreshAndSaveRecord(ctx, callbackID, call, rec); rErr == nil {
			rec = refreshed
			_, err = callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, cnCheckinStatusPath, []byte("{}"), &st)
		}
	}
	if err != nil {
		if apiErr, ok := err.(*apiCallError); ok && apiErr.code == 1001 {
			res.Outcome = "done"
			res.Message = "今日已签到"
			return res, rec, nil
		}
		return res, rec, err
	}
	if !st.Active {
		res.Outcome = "inactive"
		res.Message = "当前签到活动未开启"
		return res, rec, nil
	}
	checked := st.TodayCheckedIn || st.TodayCheckedInCC
	credit := float64(st.TodayCredit)
	if credit <= 0 {
		credit = float64(st.DailyCredit)
	}
	res.Credit = credit
	res.StreakDays = st.StreakDays
	if checked {
		res.Outcome = "done"
		res.Message = "今日已签到"
	} else {
		res.Outcome = "pending"
		res.Message = "今日待签到"
	}
	return res, rec, nil
}

func (s *Service) executeCNCheckin(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord) (DailyActionResult, hostAuthRecord) {
	res, rec, err := s.probeCNCheckin(ctx, callbackID, call, rec)
	if err != nil {
		res.Outcome = "failed"
		res.Message = err.Error()
		res.PerformedAt = time.Now().UTC()
		return res, rec
	}
	if res.Outcome == "done" || res.Outcome == "inactive" {
		return res, rec
	}

	const maxAttempts = 3
	for attempt := range maxAttempts {
		var got cnCheckinResultDTO
		_, err := callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, cnDailyCheckinPath, []byte("{}"), &got)
		if err == nil {
			res.Outcome = "claimed"
			res.Credit = float64(got.Credit)
			if res.Credit <= 0 {
				res.Credit = 100
			}
			res.StreakDays = got.StreakDays
			if strings.TrimSpace(got.Message) != "" {
				res.Message = got.Message
			} else {
				res.Message = fmt.Sprintf("签到成功，获得 %.0f 积分", res.Credit)
			}
			res.PerformedAt = time.Now().UTC()
			return res, rec
		}

		apiErr, ok := err.(*apiCallError)
		if !ok {
			res.Outcome = "failed"
			res.Message = err.Error()
			res.PerformedAt = time.Now().UTC()
			return res, rec
		}

		switch apiErr.code {
		case 1001, 10001:
			res.Outcome = "done"
			res.Message = "今日已签到"
			res.PerformedAt = time.Now().UTC()
			return res, rec
		case 1002:
			res.Outcome = "ineligible"
			res.Message = nonEmptyOr(apiErr.message, "当前账号暂不具备签到资格")
			res.PerformedAt = time.Now().UTC()
			return res, rec
		case 1003:
			res.Outcome = "inactive"
			res.Message = nonEmptyOr(apiErr.message, "当前签到活动未开启")
			res.PerformedAt = time.Now().UTC()
			return res, rec
		}

		if apiErr.status == http.StatusUnauthorized || apiErr.invalidToken {
			var probe resourceSummaryDTO
			if _, probeErr := callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, resourceSummaryPath, []byte("{}"), &probe); probeErr == nil {
				res.Outcome = "ineligible"
				res.Message = "账号凭证有效，但该账号签到受限于官方客户端环境"
				res.PerformedAt = time.Now().UTC()
				return res, rec
			}
			if refreshed, rErr := s.refreshAndSaveRecord(ctx, callbackID, call, rec); rErr == nil {
				rec = refreshed
				continue
			}
			res.Outcome = "failed"
			res.Message = "凭证已失效，请重新登录"
			res.PerformedAt = time.Now().UTC()
			return res, rec
		}

		transient := apiErr.status >= 500 || apiErr.code == 10000
		if transient && attempt+1 < maxAttempts {
			if sleepCtx(ctx, 250*time.Millisecond) != nil {
				res.Outcome = "failed"
				res.Message = "签到请求已取消"
				res.PerformedAt = time.Now().UTC()
				return res, rec
			}
			continue
		}

		res.Outcome = "failed"
		res.Message = apiErr.Error()
		res.PerformedAt = time.Now().UTC()
		return res, rec
	}

	res.Outcome = "failed"
	res.Message = "签到重试次数已耗尽"
	res.PerformedAt = time.Now().UTC()
	return res, rec
}

type heatmapCellDTO struct {
	Date        string `json:"date"`
	Score       int    `json:"score"`
	HasNewBuddy bool   `json:"has_new_buddy"`
}

type heatmapDTO struct {
	Cells []heatmapCellDTO `json:"cells"`
}

func (s *Service) probeGlobalHeatmap(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord) (DailyActionResult, hostAuthRecord, error) {
	now := time.Now().UTC()
	res := DailyActionResult{
		AuthIndex:   rec.AuthIndex,
		AuthID:      rec.Auth.ID,
		Label:       rec.Auth.Label,
		Site:        rec.Site.id,
		Kind:        "daily_bonus",
		PerformedAt: now,
	}
	var hm heatmapDTO
	_, err := callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodGet, intlHeatmapPath, nil, &hm)
	if err != nil && isAPIUnauthorized(err) {
		if refreshed, rErr := s.refreshAndSaveRecord(ctx, callbackID, call, rec); rErr == nil {
			rec = refreshed
			_, err = callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodGet, intlHeatmapPath, nil, &hm)
		}
	}
	if err != nil {
		return res, rec, err
	}
	todayBJ := time.Now().In(beijingLocation).Format("2006-01-02")
	var cell *heatmapCellDTO
	for i := range hm.Cells {
		if hm.Cells[i].Date == todayBJ {
			cell = &hm.Cells[i]
			break
		}
	}
	if cell == nil && len(hm.Cells) > 0 {
		cell = &hm.Cells[len(hm.Cells)-1]
	}
	if cell != nil {
		res.TodayDate = cell.Date
		res.TodayScore = cell.Score
		if cell.Score > 0 {
			res.Outcome = "already_lit"
			res.Message = fmt.Sprintf("今日已点亮（score=%d），奖励次日自动发放", cell.Score)
			return res, rec, nil
		}
	}
	res.Outcome = "pending"
	res.Message = "今日成长热力图未点亮"
	return res, rec, nil
}

func (s *Service) executeGlobalDailyBonus(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), rec hostAuthRecord) (DailyActionResult, hostAuthRecord) {
	res, rec, err := s.probeGlobalHeatmap(ctx, callbackID, call, rec)
	if err != nil {
		res.Outcome = "failed"
		res.Message = err.Error()
		res.PerformedAt = time.Now().UTC()
		return res, rec
	}
	if res.Outcome == "already_lit" {
		return res, rec
	}

	payload, err := buildGrowthReportPayload(rec.Stored.UID, time.Now().UnixMilli())
	if err != nil {
		res.Outcome = "failed"
		res.Message = err.Error()
		res.PerformedAt = time.Now().UTC()
		return res, rec
	}

	// Never blindly retry /v2/report on ambiguous failures to avoid double-counting score.
	_, err = callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, intlReportPath, payload, nil)
	if err != nil && isAPIUnauthorized(err) {
		if refreshed, rErr := s.refreshAndSaveRecord(ctx, callbackID, call, rec); rErr == nil {
			rec = refreshed
			_, err = callWorkBuddyAPI(ctx, rec, callbackID, call, http.MethodPost, intlReportPath, payload, nil)
		}
	}
	res.PerformedAt = time.Now().UTC()
	if err != nil {
		res.Outcome = "failed"
		res.Message = err.Error()
		return res, rec
	}

	// Re-probe heatmap best-effort so score updates immediately in UI when upstream reflects it.
	if after, nextRec, pErr := s.probeGlobalHeatmap(ctx, callbackID, call, rec); pErr == nil {
		rec = nextRec
		res.TodayDate = after.TodayDate
		res.TodayScore = after.TodayScore
	}
	res.Outcome = "lit"
	res.Credit = 30
	res.Message = "已上报今日活跃点亮（Free 30 / Pro 50 积分将在次日自动到账）"
	return res, rec
}

func buildGrowthReportPayload(uid string, nowMs int64) ([]byte, error) {
	convID := "wb-" + newUUIDLikeHex()
	reqID := "wb-" + newUUIDLikeHex()
	events := []map[string]any{{
		"eventCode":            "chat_request_send",
		"timestamp":            nowMs,
		"mode":                 "craft",
		"conversationId":       convID,
		"requestId":            reqID,
		"inputLength":          12,
		"requestModelId":       "deepseek-v4-flash",
		"requestModelName":     "DeepSeek V4 Flash",
		"mentionContexts":      []string{},
		"knowledgeId":          []string{},
		"knowledgeName":        []string{},
		"presentAt":            nowMs,
		"rootRequestId":        reqID,
		"parentConversationId": convID,
		"agentName":            "default",
		"agentType":            "conversation",
		"userId":               uid,
	}}
	return json.Marshal(events)
}

func newUUIDLikeHex() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

// RunAllDailyActions executes CN check-in or Global daily bonus across all enabled WorkBuddy credentials.
// targetKind may be "all", "checkin", or "daily_bonus".
func (s *Service) RunAllDailyActions(ctx context.Context, callbackID string, call func(string, []byte) ([]byte, error), targetKind string) ([]DailyActionResult, error) {
	call = s.bindHostCall(call)
	entries, err := hostAuthList(call)
	if err != nil {
		return nil, err
	}
	results := make([]DailyActionResult, 0, len(entries))
	for _, entry := range entries {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		if entry.Disabled || entry.AuthIndex == "" {
			continue
		}
		rec, err := s.resolveAuthRecord(ctx, callbackID, call, entry.AuthIndex, nil)
		if err != nil {
			results = append(results, DailyActionResult{
				AuthIndex:   entry.AuthIndex,
				AuthID:      entry.ID,
				Label:       entry.Label,
				Outcome:     "failed",
				Message:     err.Error(),
				PerformedAt: time.Now().UTC(),
			})
			continue
		}
		switch targetKind {
		case "checkin":
			if rec.Site.id != "workbuddy" {
				continue
			}
			results = append(results, s.runAccountCheckinRecord(ctx, callbackID, call, rec))
		case "daily_bonus":
			if rec.Site.id != "workbuddy-ai" {
				continue
			}
			results = append(results, s.runAccountDailyBonusRecord(ctx, callbackID, call, rec))
		default:
			if rec.Site.id == "workbuddy" {
				results = append(results, s.runAccountCheckinRecord(ctx, callbackID, call, rec))
			} else {
				results = append(results, s.runAccountDailyBonusRecord(ctx, callbackID, call, rec))
			}
		}
	}
	return results, nil
}

func callWorkBuddyAPI(ctx context.Context, rec hostAuthRecord, callbackID string, call func(string, []byte) ([]byte, error), method, path string, body []byte, target any) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, failure("request_cancelled", "WorkBuddy request was cancelled", 408)
	}
	if call == nil {
		return nil, failure("host_unavailable", "host HTTP callback is unavailable", 503)
	}
	headers, err := rec.Auth.RequestHeaders()
	if err != nil {
		return nil, err
	}
	headers["Accept"] = []string{"application/json"}
	if method != http.MethodGet {
		headers["Content-Type"] = []string{"application/json"}
	}
	rawReq, err := json.Marshal(hostRequest{
		Method:         method,
		URL:            rec.Site.endpoint + path,
		Headers:        headers,
		Body:           body,
		HostCallbackID: callbackID,
	})
	if err != nil {
		return nil, failure("internal_error", "cannot encode host HTTP request", 500)
	}
	reply, err := call("host.http.do", rawReq)
	if ctx.Err() != nil {
		return nil, failure("request_cancelled", "WorkBuddy request was cancelled", 408)
	}
	if err != nil {
		return nil, failure("host_http_failed", "host HTTP request failed", 502)
	}
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			StatusCode int
			Headers    map[string][]string
			Body       []byte
		} `json:"result"`
	}
	if json.Unmarshal(reply, &envelope) != nil || !envelope.OK || envelope.Result.StatusCode < 100 || envelope.Result.StatusCode > 599 {
		return nil, failure("host_http_failed", "host returned an invalid HTTP response", 502)
	}

	status := envelope.Result.StatusCode
	rawBody := bytes.TrimSpace(envelope.Result.Body)
	authReason := authenticationReason(status, envelope.Result.Headers, rawBody)
	if status == http.StatusUnauthorized || authReason != "" {
		return nil, &apiCallError{
			status:       status,
			invalidToken: authReason == "invalid_token" || status == http.StatusUnauthorized,
			message:      fmt.Sprintf("authentication rejected (%d)", status),
		}
	}
	if status >= 500 || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
		return nil, &apiCallError{status: status, message: fmt.Sprintf("WorkBuddy upstream HTTP %d", status)}
	}
	if len(rawBody) == 0 {
		if status < 200 || status >= 300 {
			return nil, &apiCallError{status: status, message: fmt.Sprintf("WorkBuddy upstream HTTP %d", status)}
		}
		return nil, nil
	}

	var wrapped struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(rawBody, &wrapped) != nil {
		if status < 200 || status >= 300 {
			return nil, &apiCallError{status: status, unreadable: true, message: fmt.Sprintf("WorkBuddy upstream HTTP %d", status)}
		}
		return nil, &apiCallError{status: status, unreadable: true, message: "WorkBuddy returned non-JSON response"}
	}

	code := 0
	if len(wrapped.Code) != 0 && string(wrapped.Code) != "null" {
		val := strings.Trim(string(wrapped.Code), `"`)
		parsed, err := strconv.Atoi(val)
		if err != nil {
			return nil, &apiCallError{status: status, unreadable: true, message: "WorkBuddy returned non-numeric response code"}
		}
		code = parsed
	}
	msg := strings.TrimSpace(wrapped.Message)
	if msg == "" {
		msg = strings.TrimSpace(wrapped.Msg)
	}
	if status < 200 || status >= 300 || code != 0 {
		return nil, &apiCallError{status: status, code: code, message: msg}
	}

	payload := rawBody
	trimmedData := bytes.TrimSpace(wrapped.Data)
	if len(trimmedData) > 0 && !bytes.Equal(trimmedData, []byte("null")) {
		payload = trimmedData
	}
	if target != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, target); err != nil {
			return nil, &apiCallError{status: status, unreadable: true, message: fmt.Sprintf("decode WorkBuddy payload: %v", err)}
		}
	}
	return payload, nil
}

func isAPIUnauthorized(err error) bool {
	apiErr, ok := err.(*apiCallError)
	return ok && (apiErr.status == http.StatusUnauthorized || apiErr.invalidToken)
}

func toPublicError(err error) error {
	if e, ok := err.(*Error); ok {
		return e
	}
	if apiErr, ok := err.(*apiCallError); ok {
		status := apiErr.status
		if status == 0 || (status >= 200 && status < 300) {
			status = 502
		}
		return failure("upstream_error", apiErr.Error(), status)
	}
	return failure("upstream_error", err.Error(), 502)
}

func parseBeijingTime(value string) (time.Time, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", v, beijingLocation); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func packageEndTime(p resourcePackageDTO) time.Time {
	rawMs := strings.Trim(strings.TrimSpace(string(p.DeductionEndTime)), `"`)
	if rawMs != "" && rawMs != "null" {
		if ms, err := strconv.ParseInt(rawMs, 10, 64); err == nil && ms > 0 {
			return time.UnixMilli(ms)
		}
	}
	for _, candidate := range []string{p.ExpiredTime, p.CycleEndTime} {
		if t, ok := parseBeijingTime(candidate); ok {
			return t
		}
	}
	return time.Time{}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nonEmptyOr(val, fallback string) string {
	if strings.TrimSpace(val) != "" {
		return val
	}
	return fallback
}
