package workbuddy

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const chatErrorLimit = 64 << 10

type upstreamFailure struct {
	codes []string
}

func readUpstreamFailure(body []byte) upstreamFailure {
	var result upstreamFailure
	var visit func(json.RawMessage, int)
	visit = func(raw json.RawMessage, depth int) {
		if depth > 8 {
			return
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(raw, &value) != nil || value == nil {
			var text string
			if json.Unmarshal(raw, &text) == nil {
				if code, _, ok := strings.Cut(text, ":"); ok && upstreamCodeValue(code) {
					result.codes = append(result.codes, code)
				}
			}
			return
		}
		for _, name := range [...]string{"internal_code", "code", "type"} {
			var code string
			if json.Unmarshal(value[name], &code) != nil {
				var number json.Number
				if json.Unmarshal(value[name], &number) == nil {
					code = number.String()
				}
			}
			if upstreamCodeValue(code) {
				result.codes = append(result.codes, code)
			}
		}
		visit(value["error"], depth+1)
		visit(value["data"], depth+1)
	}
	visit(body, 0)
	return result
}

func upstreamCodeValue(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func (e upstreamFailure) has(code string) bool {
	return slices.Contains(e.codes, code)
}

func chatCorrectionSignals(status int, body []byte) (refused, afterTools bool) {
	if status != http.StatusBadRequest {
		return false, false
	}
	text := strings.ToLower(string(body))
	detail := readUpstreamFailure(body)
	refused = detail.has("11128") || strings.Contains(text, "unapproved channel") || strings.Contains(text, "illegal api invocation")
	afterTools = detail.has("11153") || strings.Contains(text, "must end with a user message")
	return refused, afterTools
}

func chatHTTPFailure(status int, headers map[string][]string, body []byte) *Error {
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	upstream := readUpstreamFailure(body)
	text := strings.ToLower(string(body))
	code, message := "upstream_http_failed", "WorkBuddy request failed"
	matched := ""
	switch {
	case upstream.has("10085"):
		code, message, status, matched = "client_version_rejected", "WorkBuddy rejected the client version", http.StatusBadGateway, "10085"
	case upstream.has("11140") || strings.Contains(text, "account has been restricted due to unusual activity") || strings.Contains(text, "账号存在异常行为") || strings.Contains(text, "帳號存在異常行為"):
		code, message, matched = "account_restricted", "WorkBuddy restricted this account; account review is required", "11140"
	case upstream.has("14018") || strings.Contains(text, "credits exhausted") || strings.Contains(text, "额度已用尽"):
		code, message, matched = "credit_package_exhausted", "WorkBuddy requires an active credit package with available balance", "14018"
	case upstream.has("11159") || messagesSignatureRejected(body):
		code, message, matched = "thinking_signature_rejected", "WorkBuddy rejected a Messages thinking signature", "11159"
	case upstream.has("11153"):
		code, message, matched = "tool_continuation_rejected", "WorkBuddy rejected the tool continuation shape", "11153"
	case upstream.has("11128") || strings.Contains(text, "unapproved channel") || strings.Contains(text, "illegal api invocation"):
		code, message, matched = "channel_rejected", "WorkBuddy rejected the client prompt shape", "11128"
	case upstream.has("context_length_exceeded") || upstream.has("context_window_exceeded") || strings.Contains(text, "maximum context length") || strings.Contains(text, "context window exceeded") || strings.Contains(text, "上下文超限"):
		code, message = "context_length_exceeded", "WorkBuddy rejected a request exceeding the model context limit"
	case status == http.StatusForbidden:
		code, message = "model_permission_denied", "WorkBuddy denied access to this model"
	case status == http.StatusTooManyRequests:
		code, message = "rate_limit_exceeded", "WorkBuddy rate limit exceeded"
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		code, message = "upstream_timeout", "WorkBuddy request timed out"
	case status >= 500:
		code, message = "upstream_unavailable", "WorkBuddy service is temporarily unavailable"
	}
	if matched == "" {
		for _, candidate := range upstream.codes {
			if _, err := strconv.ParseUint(candidate, 10, 32); err == nil && len(candidate) <= 9 {
				matched = candidate
				break
			}
		}
	}
	if matched != "" {
		message += " (upstream code " + matched + ")"
	}
	return &Error{Code: code, Message: message, HTTPStatus: status, UpstreamCode: matched, RetryAfter: safeRetryAfter(chatHeader(headers, "Retry-After"))}
}

func safeRetryAfter(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 128 || !safeValue(value) {
		return ""
	}
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil && seconds <= uint64((1<<63-1)/time.Second) {
		return value
	}
	if _, err := http.ParseTime(value); err == nil {
		return value
	}
	return ""
}
