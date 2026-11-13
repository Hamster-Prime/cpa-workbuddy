package workbuddy

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// CatalogModel retains account-discovered capabilities and credit multipliers.
// Credit values are plan credits, never currency prices; nil means unknown.
type CatalogModel struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ContextLength   int64    `json:"context_length"`
	MaxOutputTokens int64    `json:"max_output_tokens"`
	Images          bool     `json:"images"`
	Efforts         []string `json:"efforts,omitempty"`
	DefaultEffort   string   `json:"default_effort,omitempty"`
	CreditRate      *float64 `json:"credit_rate,omitempty"`
	CreditRateWas   *float64 `json:"credit_rate_was,omitempty"`
	Free            *bool    `json:"free,omitempty"`
}

type catalogConfig struct {
	Agents          []*catalogAgent     `json:"agents"`
	Models          []*catalogDetail    `json:"models"`
	ModelPromotions []*catalogPromotion `json:"modelPromotions"`
}

type catalogAgent struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

type catalogDetail struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	MaxInputTokens    *int64            `json:"maxInputTokens"`
	MaxOutputTokens   *int64            `json:"maxOutputTokens"`
	SupportsImages    bool              `json:"supportsImages"`
	SupportsReasoning bool              `json:"supportsReasoning"`
	OnlyReasoning     bool              `json:"onlyReasoning"`
	Reasoning         *catalogReasoning `json:"reasoning"`
	Credits           json.RawMessage   `json:"credits"`
	credit            *float64
}

type catalogReasoning struct {
	SupportedEfforts   []string `json:"supportedEfforts"`
	DefaultEffort      *string  `json:"defaultEffort"`
	Effort             *string  `json:"effort"`
	CanDisableThinking *bool    `json:"canDisableThinking"`
}

type catalogPromotion struct {
	Kind     string           `json:"kind"`
	ModelIDs []string         `json:"modelIds"`
	Enabled  *bool            `json:"enabled"`
	Discount *catalogDiscount `json:"discount"`
	Schedule *catalogSchedule `json:"schedule"`
}

type catalogDiscount struct {
	DiscountedCredits json.RawMessage `json:"discountedCredits"`
	Factor            json.RawMessage `json:"factor"`
	rate              *float64
	factor            *float64
}

type catalogSchedule struct {
	ValidFrom  string            `json:"validFrom"`
	ValidUntil string            `json:"validUntil"`
	Daily      []json.RawMessage `json:"daily"`
}

// ParseModelConfig parses the data object returned by /v3/config. It preserves
// CLI order, then appends enterprise custom: models in configuration order.
// A valid empty CLI list returns an allocated empty slice. Missing CLI access
// and malformed configuration return distinct, safe typed errors.
func ParseModelConfig(siteID string, rawData []byte, now time.Time) ([]CatalogModel, error) {
	if _, ok := selectSite(siteID); !ok {
		return nil, failure("invalid_site", "model discovery requires workbuddy or workbuddy-ai", 400)
	}
	invalid := func() error {
		return failure("invalid_model_config", "WorkBuddy returned an invalid model configuration", 502)
	}
	rawData = bytes.TrimSpace(rawData)
	if len(rawData) == 0 || rawData[0] != '{' {
		return nil, invalid()
	}
	var cfg catalogConfig
	if err := json.Unmarshal(rawData, &cfg); err != nil {
		return nil, invalid()
	}
	for _, agent := range cfg.Agents {
		if agent == nil || agent.Name == "" {
			return nil, invalid()
		}
		for _, id := range agent.Models {
			if !catalogID(id) {
				return nil, invalid()
			}
		}
	}
	details := make(map[string]*catalogDetail, len(cfg.Models))
	for _, detail := range cfg.Models {
		if detail == nil || !catalogID(detail.ID) ||
			(detail.MaxInputTokens != nil && *detail.MaxInputTokens < 0) ||
			(detail.MaxOutputTokens != nil && *detail.MaxOutputTokens < 0) {
			return nil, invalid()
		}
		if detail.Reasoning != nil {
			for _, effort := range detail.Reasoning.SupportedEfforts {
				if !catalogID(effort) {
					return nil, invalid()
				}
			}
		}
		var err error
		detail.credit, err = catalogCredit(detail.Credits, false)
		if err != nil {
			return nil, invalid()
		}
		if _, exists := details[detail.ID]; !exists {
			details[detail.ID] = detail
		}
	}
	for _, promotion := range cfg.ModelPromotions {
		if promotion == nil {
			return nil, invalid()
		}
		for _, id := range promotion.ModelIDs {
			if !catalogID(id) {
				return nil, invalid()
			}
		}
		if promotion.Discount != nil {
			var err error
			promotion.Discount.rate, err = catalogCredit(promotion.Discount.DiscountedCredits, true)
			if err != nil {
				return nil, invalid()
			}
			promotion.Discount.factor, err = catalogFactor(promotion.Discount.Factor)
			if err != nil {
				return nil, invalid()
			}
		}
	}

	out := make([]CatalogModel, 0)
	seen := make(map[string]bool)
	add := func(id string, detail *catalogDetail) {
		if seen[id] {
			return
		}
		seen[id] = true
		model := CatalogModel{ID: id, Name: id}
		if detail != nil {
			if detail.Name != "" {
				model.Name = detail.Name
			}
			if detail.MaxInputTokens != nil {
				model.ContextLength = *detail.MaxInputTokens
			}
			if detail.MaxOutputTokens != nil {
				model.MaxOutputTokens = *detail.MaxOutputTokens
			}
			model.Images = detail.SupportsImages && (siteID != "workbuddy-ai" || !strings.HasPrefix(id, "glm-"))
			model.Efforts, model.DefaultEffort = catalogEfforts(siteID, id, detail)
			model.CreditRate, model.CreditRateWas, model.Free = catalogPrice(detail, cfg.ModelPromotions, now)
		}
		out = append(out, model)
	}
	cli := false
	for _, agent := range cfg.Agents {
		if agent.Name != "cli" {
			continue
		}
		cli = true
		for _, id := range agent.Models {
			add(id, details[id])
		}
	}
	if !cli {
		return nil, failure("missing_cli_config", "WorkBuddy configuration has no CLI agent", 502)
	}
	for _, detail := range cfg.Models {
		if strings.HasPrefix(detail.ID, "custom:") {
			add(detail.ID, detail)
		}
	}
	first := make(map[string]string, len(out))
	for i := range out {
		model := &out[i]
		id, exists := first[model.Name]
		if !exists {
			first[model.Name] = model.ID
			continue
		}
		rest := strings.TrimPrefix(model.ID, id+"-")
		if rest == model.ID || rest == "" {
			rest = model.ID
		} else {
			rest = strings.ToUpper(rest)
		}
		model.Name += " (" + rest + ")"
	}
	return out, nil
}

func catalogID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}

// messagesModel is the reference source route classifier, independent of the
// public capabilities enabled by the current execution implementation.
func messagesModel(siteID, id string) bool {
	return siteID == "workbuddy" && len(id) >= len("claude-") && strings.EqualFold(id[:len("claude-")], "claude-")
}

func catalogEfforts(siteID, id string, detail *catalogDetail) ([]string, string) {
	r := detail.Reasoning
	off := (!detail.OnlyReasoning && (r == nil || r.CanDisableThinking == nil || *r.CanDisableThinking)) ||
		id == "deepseek-v4.1-flash" || id == "deepseek-v4.1-flash-sg"
	var efforts []string
	if r != nil && len(r.SupportedEfforts) != 0 {
		efforts = make([]string, 0, len(r.SupportedEfforts)+2)
		if off {
			efforts = append(efforts, "none")
		}
		efforts = append(efforts, r.SupportedEfforts...)
		if messagesModel(siteID, id) && !slices.Contains(efforts, "max") {
			efforts = append(efforts, "max")
		}
	} else if detail.SupportsReasoning {
		efforts = make([]string, 0, 6)
		if off {
			efforts = append(efforts, "none")
		}
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
			if effort == "low" || effort == "high" || effort == "max" || (r != nil && r.Effort != nil && *r.Effort == effort) {
				efforts = append(efforts, effort)
			}
		}
	}
	if len(efforts) == 0 {
		return nil, ""
	}
	levels := efforts
	if off {
		levels = levels[1:]
	}
	defaultEffort := ""
	if r != nil {
		own := r.DefaultEffort
		if own == nil {
			own = r.Effort
		}
		if own != nil && slices.Contains(levels, *own) {
			defaultEffort = *own
		}
	}
	return efforts, defaultEffort
}

func catalogCredit(raw json.RawMessage, trailingX bool) (*float64, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var text string
	switch raw[0] {
	case '"':
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
	case 't', 'f':
		return nil, nil
	default:
		var number float64
		if err := json.Unmarshal(raw, &number); err != nil {
			return nil, err
		}
		if number >= 0 && !math.IsInf(number, 0) {
			return &number, nil
		}
		return nil, nil
	}
	text = strings.ToLower(strings.TrimSpace(text))
	if trailingX {
		text = strings.TrimSuffix(text, "x")
	}
	for _, suffix := range []string{"credits", "credit"} {
		if prefix, found := strings.CutSuffix(text, suffix); found {
			trimmed := strings.TrimRightFunc(prefix, unicode.IsSpace)
			if len(trimmed) < len(prefix) {
				text = trimmed
			}
			break
		}
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "x"))
	value, err := strconv.ParseFloat(text, 64)
	if err == nil && value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
		return &value, nil
	}
	// An unrecognized price is unknown, so it cannot imply free service.
	return nil, nil
}

func catalogFactor(raw json.RawMessage) (*float64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return new(float64(0)), nil
	}
	if bytes.Equal(raw, []byte("true")) {
		return new(float64(1)), nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return new(float64(0)), nil
		}
		value, err := strconv.ParseFloat(text, 64)
		if err == nil && value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
			return &value, nil
		}
		return nil, nil
	}
	return catalogCredit(raw, false)
}

func catalogRunning(promotion *catalogPromotion, now time.Time) bool {
	if promotion.Enabled != nil && !*promotion.Enabled {
		return false
	}
	if promotion.Schedule != nil {
		if from, err := time.Parse(time.RFC3339Nano, promotion.Schedule.ValidFrom); err == nil && now.Before(from) {
			return false
		}
		if until, err := time.Parse(time.RFC3339Nano, promotion.Schedule.ValidUntil); err == nil && !now.Before(until) {
			return false
		}
	}
	return true
}

func catalogDiscountRate(discount *catalogDiscount, base *float64) *float64 {
	if discount.rate != nil {
		return discount.rate
	}
	if discount.factor != nil && base != nil {
		value := *discount.factor * *base
		if !math.IsInf(value, 0) {
			return &value
		}
	}
	return nil
}

func catalogPrice(detail *catalogDetail, promotions []*catalogPromotion, now time.Time) (rate, was *float64, free *bool) {
	daily := false
	nightlyZero := false
	var discounted *float64
	for _, promotion := range promotions {
		if promotion.Kind != "discount" || !slices.Contains(promotion.ModelIDs, detail.ID) || !catalogRunning(promotion, now) {
			continue
		}
		byHour := promotion.Schedule != nil && len(promotion.Schedule.Daily) > 0
		daily = daily || byHour
		if promotion.Discount == nil {
			continue
		}
		value := catalogDiscountRate(promotion.Discount, detail.credit)
		if value != nil {
			if byHour && *value == 0 {
				nightlyZero = true
			}
			if discounted == nil || *value < *discounted {
				discounted = value
			}
		}
	}
	if daily {
		value := false
		if detail.credit == nil || (*detail.credit == 0 && nightlyZero) {
			return nil, nil, &value
		}
		return detail.credit, nil, &value
	}
	if discounted != nil {
		value := *discounted == 0
		if detail.credit != nil && *detail.credit > *discounted {
			was = detail.credit
		}
		return discounted, was, &value
	}
	if detail.credit == nil {
		return nil, nil, nil
	}
	value := *detail.credit == 0
	return detail.credit, nil, &value
}
