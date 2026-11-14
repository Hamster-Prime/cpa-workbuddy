package workbuddy

import (
	"encoding/json"
	"regexp"
	"strings"
)

var invalidTokenChallenge = regexp.MustCompile(`(?i)(?:^|[ ,])error\s*=\s*"invalid_token"(?:\s*,|\s*$)`)

func authenticationReason(status int, headers map[string][]string, body []byte) string {
	if status < 400 || status >= 500 {
		return ""
	}
	if status == 401 {
		for key, values := range headers {
			if !strings.EqualFold(key, "WWW-Authenticate") {
				continue
			}
			for _, value := range values {
				scheme, _, found := strings.Cut(strings.TrimSpace(value), " ")
				if found && strings.EqualFold(scheme, "Bearer") && invalidTokenChallenge.MatchString(value) {
					return "invalid_token"
				}
			}
		}
	}
	var response struct {
		Code  json.RawMessage `json:"code"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	if status == 401 && (string(response.Code) == "12153" || string(response.Code) == `"12153"`) {
		return "invalid_grant"
	}
	var name string
	if json.Unmarshal(response.Error, &name) != nil {
		var detail struct {
			Type string `json:"type"`
			Code string `json:"code"`
		}
		if json.Unmarshal(response.Error, &detail) != nil {
			return ""
		}
		name = detail.Code
		if name == "" {
			name = detail.Type
		}
	}
	switch name {
	case "invalid_grant":
		return "invalid_grant"
	case "revoked", "token_revoked":
		return "revoked"
	case "invalid_token":
		if status == 401 {
			return "invalid_token"
		}
	}
	return ""
}
