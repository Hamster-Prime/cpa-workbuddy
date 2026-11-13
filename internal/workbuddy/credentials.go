package workbuddy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"
	"time"
)

const credentialVersion = 1

// AuthData mirrors CPA sdk/pluginapi.AuthData without a dependency on its host
// implementation. StorageJSON is a JSON byte slice, hence base64 on the wire.
type AuthData struct {
	Provider         string
	ID               string
	FileName         string
	Label            string
	Prefix           string
	ProxyURL         string
	Disabled         bool
	StorageJSON      []byte
	Metadata         map[string]any
	Attributes       map[string]string
	NextRefreshAfter time.Time
}

type ParseResponse struct {
	Handled bool
	Auth    AuthData
	Auths   []AuthData
}

type tokenData struct {
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	Domain           string `json:"domain"`
	TokenType        string `json:"tokenType"`
	ExpiresAt        int64  `json:"expiresAt"`
	ExpiresIn        int64  `json:"expiresIn"`
	RefreshExpiresAt int64  `json:"refreshExpiresAt"`
	RefreshExpiresIn int64  `json:"refreshExpiresIn"`
}

func (t tokenData) empty() bool { return t == (tokenData{}) }

func safeValue(value string) bool {
	return !strings.ContainsAny(value, "\r\n\x00")
}

func requestDomain(chosen site, domain string) string {
	if domain != "" {
		return domain
	}
	return strings.TrimPrefix(chosen.endpoint, "https://")
}

func (t tokenData) valid() bool {
	return strings.TrimSpace(t.AccessToken) != "" && safeValue(t.AccessToken) && safeValue(t.RefreshToken) && safeValue(t.Domain) && safeValue(t.TokenType) &&
		(t.ExpiresAt > 0 || (t.ExpiresIn > 0 && t.ExpiresIn <= int64((100*365*24*time.Hour)/time.Second))) &&
		t.RefreshExpiresAt >= 0 && t.RefreshExpiresIn >= 0 && t.RefreshExpiresIn <= int64((100*365*24*time.Hour)/time.Second)
}

func (t *tokenData) resolveExpiry(now time.Time) {
	if t.ExpiresAt <= 0 {
		t.ExpiresAt = now.Add(time.Duration(t.ExpiresIn) * time.Second).UnixMilli()
	}
	if t.RefreshExpiresAt <= 0 && t.RefreshExpiresIn > 0 {
		t.RefreshExpiresAt = now.Add(time.Duration(t.RefreshExpiresIn) * time.Second).UnixMilli()
	}
}

type account struct {
	UID            string `json:"uid"`
	Nickname       string `json:"nickname"`
	PhoneNumber    string `json:"phoneNumber"`
	Type           string `json:"type"`
	EnterpriseID   string `json:"enterpriseId"`
	EnterpriseName string `json:"enterpriseName"`
	LastLogin      bool   `json:"lastLogin"`
}

func (a account) empty() bool { return a == (account{}) }

type enterpriseAccount struct {
	id   string
	name string
}

func enterprise(ctx context.Context, chosen site, callbackID string, call func(string, []byte) ([]byte, error), token *tokenData, who account) (enterpriseAccount, error) {
	own := strings.TrimSpace(who.EnterpriseID)
	kind := strings.ToLower(strings.TrimSpace(who.Type))
	if own == "" && (kind == "" || kind == "personal") {
		return enterpriseAccount{}, nil
	}
	name := strings.TrimSpace(who.EnterpriseName)
	if own != "" && name != "" {
		return enterpriseAccount{id: own, name: name}, nil
	}
	var list struct {
		Accounts []account `json:"accounts"`
	}
	err := request(ctx, chosen, callbackID, call, "GET", "/v2/plugin/accounts", map[string][]string{
		"Authorization": {"Bearer " + token.AccessToken},
		"X-Domain":      {requestDomain(chosen, token.Domain)},
	}, nil, &list)
	if err != nil {
		// The login account's explicit enterprise ID is authoritative. A
		// missing display name does not change the chosen scope.
		if own != "" && ctx.Err() == nil {
			return enterpriseAccount{id: own}, nil
		}
		return enterpriseAccount{}, err
	}
	var mine, same, last []account
	for _, candidate := range list.Accounts {
		if candidate.UID != who.UID || strings.TrimSpace(candidate.EnterpriseID) == "" {
			continue
		}
		if own != "" && strings.TrimSpace(candidate.EnterpriseID) == own {
			return enterpriseAccount{id: own, name: strings.TrimSpace(candidate.EnterpriseName)}, nil
		}
		mine = append(mine, candidate)
		if strings.ToLower(strings.TrimSpace(candidate.Type)) == kind {
			same = append(same, candidate)
		}
		if candidate.LastLogin {
			last = append(last, candidate)
		}
	}
	if own != "" {
		return enterpriseAccount{id: own}, nil
	}
	var picked account
	switch {
	case len(same) == 1:
		picked = same[0]
	case len(last) == 1:
		picked = last[0]
	case len(mine) == 1:
		picked = mine[0]
	default:
		return enterpriseAccount{}, failure("ambiguous_enterprise", "WorkBuddy did not identify the selected enterprise account", 502)
	}
	return enterpriseAccount{id: strings.TrimSpace(picked.EnterpriseID), name: strings.TrimSpace(picked.EnterpriseName)}, nil
}

// The host overwrites type with the provider during persistence. A separate
// credential_type marker distinguishes our versioned browser credentials from
// API keys and desktop files, including files that happen to contain a token.
type credential struct {
	Provider         string     `json:"provider"`
	Type             string     `json:"type"`
	CredentialType   string     `json:"credential_type"`
	Version          int        `json:"version"`
	Site             string     `json:"site"`
	UID              string     `json:"uid"`
	EnterpriseID     string     `json:"enterprise_id"`
	DisplayName      string     `json:"display_name"`
	AccessToken      string     `json:"access_token"`
	RefreshToken     string     `json:"refresh_token"`
	Domain           string     `json:"domain"`
	TokenType        string     `json:"token_type"`
	ExpiresAt        time.Time  `json:"expires_at"`
	RefreshExpiresAt *time.Time `json:"refresh_expires_at"`
}

func makeAuth(chosen site, token tokenData, who account, ent enterpriseAccount) (AuthData, error) {
	person := who.Nickname
	if person == "" {
		person = who.PhoneNumber
	}
	if person == "" {
		person = who.UID
	}
	if ent.id != "" {
		name := ent.name
		if name == "" {
			id := ent.id
			if len(id) > 6 {
				id = id[len(id)-6:]
			}
			name = "enterprise " + id
		}
		person += " · " + name
	}
	stored := credential{
		Provider: provider, Type: provider, CredentialType: "browser", Version: credentialVersion,
		Site: chosen.id, UID: strings.TrimSpace(who.UID), EnterpriseID: ent.id,
		DisplayName: person, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		Domain: token.Domain, TokenType: token.TokenType, ExpiresAt: time.UnixMilli(token.ExpiresAt).UTC(),
	}
	if token.RefreshExpiresAt > 0 {
		expires := time.UnixMilli(token.RefreshExpiresAt).UTC()
		stored.RefreshExpiresAt = &expires
	}
	return authFromCredential(stored, nil)
}

func credentialSite(stored credential) (site, error) {
	chosen, ok := selectSite(stored.Site)
	if !ok || strings.TrimSpace(stored.UID) == "" || stored.UID != strings.TrimSpace(stored.UID) ||
		strings.TrimSpace(stored.AccessToken) == "" || stored.ExpiresAt.IsZero() || stored.ExpiresAt.UnixMilli() <= 0 ||
		stored.EnterpriseID != strings.TrimSpace(stored.EnterpriseID) || !safeValue(stored.UID) || !safeValue(stored.EnterpriseID) ||
		!safeValue(stored.AccessToken) || !safeValue(stored.RefreshToken) || !safeValue(stored.Domain) || !safeValue(stored.TokenType) ||
		(stored.RefreshExpiresAt != nil && (stored.RefreshExpiresAt.IsZero() || stored.RefreshExpiresAt.UnixMilli() <= 0)) {
		return site{}, failure("invalid_credentials", "invalid WorkBuddy browser credentials", 400)
	}
	return chosen, nil
}

func credentialIdentity(stored credential) (string, string, error) {
	scope := "personal"
	if stored.EnterpriseID != "" {
		scope = "enterprise:" + stored.EnterpriseID
	}
	identity, err := json.Marshal([3]string{stored.Site, stored.UID, scope})
	if err != nil {
		return "", "", failure("internal_error", "cannot encode browser account identity", 500)
	}
	digest := sha256.Sum256(identity)
	return "workbuddy-" + hex.EncodeToString(digest[:]), scope, nil
}

func authFromCredential(stored credential, metadata map[string]any) (AuthData, error) {
	chosen, err := credentialSite(stored)
	if err != nil {
		return AuthData{}, err
	}
	id, scope, err := credentialIdentity(stored)
	if err != nil {
		return AuthData{}, err
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return AuthData{}, failure("invalid_credentials", "invalid WorkBuddy browser credential expiry", 400)
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	var own map[string]any
	if err := json.Unmarshal(raw, &own); err != nil {
		return AuthData{}, failure("internal_error", "cannot encode browser credentials", 500)
	}
	maps.Copy(metadata, own)
	label := chosen.name + " · " + stored.DisplayName
	if stored.DisplayName == "" {
		label = chosen.name
	}
	disabled, _ := metadata["disabled"].(bool)
	proxy, _ := metadata["proxy_url"].(string)
	return AuthData{
		Provider: provider, ID: id, FileName: id + ".json", Label: label, ProxyURL: proxy, Disabled: disabled,
		StorageJSON: raw, Metadata: metadata,
		Attributes: map[string]string{"site": stored.Site, "uid": stored.UID, "scope": scope},
	}, nil
}

func parse(raw []byte) (any, error) {
	var req struct {
		Provider string
		RawJSON  []byte
	}
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.Provider != provider {
		return ParseResponse{}, nil
	}
	stored, metadata, handled, err := credentialFromJSON(req.RawJSON)
	if err != nil {
		return nil, err
	}
	if !handled {
		return ParseResponse{}, nil
	}
	auth, err := authFromCredential(stored, metadata)
	if err != nil {
		return nil, err
	}
	return ParseResponse{Handled: true, Auth: auth}, nil
}

func credentialFromJSON(raw []byte) (credential, map[string]any, bool, error) {
	var marker struct {
		Provider       string `json:"provider"`
		Type           string `json:"type"`
		CredentialType string `json:"credential_type"`
		Version        int    `json:"version"`
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return credential{}, nil, false, failure("invalid_credentials", "invalid browser credential markers", 400)
	}
	if marker.Provider != provider || marker.Type != provider || marker.CredentialType != "browser" || marker.Version != credentialVersion {
		return credential{}, nil, false, nil
	}
	var stored credential
	if err := json.Unmarshal(raw, &stored); err != nil {
		return credential{}, nil, true, failure("invalid_credentials", "invalid WorkBuddy browser credentials", 400)
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return credential{}, nil, true, failure("invalid_credentials", "invalid WorkBuddy browser credentials", 400)
	}
	for _, field := range []string{"api_key", "apiKey", "endpoint", "base_url", "baseURL", "authID"} {
		if _, exists := metadata[field]; exists {
			return credential{}, nil, true, failure("invalid_credentials", "unsupported WorkBuddy browser credential fields", 400)
		}
	}
	return stored, metadata, true, nil
}

// RequestHeaders binds authenticated requests to this persisted identity. Host
// metadata and request headers cannot override its selected enterprise scope.
func (auth AuthData) RequestHeaders() (map[string][]string, error) {
	stored, _, handled, err := credentialFromJSON(auth.StorageJSON)
	if err != nil {
		return nil, err
	}
	if auth.Provider != provider || !handled {
		return nil, failure("invalid_credentials", "unsupported WorkBuddy browser credentials", 400)
	}
	chosen, err := credentialSite(stored)
	if err != nil {
		return nil, err
	}
	id, _, err := credentialIdentity(stored)
	if err != nil {
		return nil, err
	}
	if auth.ID != id || (auth.FileName != "" && auth.FileName != id+".json") {
		return nil, failure("identity_mismatch", "credential scope does not match the selected account", 400)
	}
	headers := map[string][]string{
		"Authorization": {"Bearer " + stored.AccessToken},
		"X-User-Id":     {stored.UID},
		"X-Domain":      {requestDomain(chosen, stored.Domain)},
	}
	if stored.EnterpriseID != "" {
		headers["X-Enterprise-Id"] = []string{stored.EnterpriseID}
		headers["X-Tenant-Id"] = []string{stored.EnterpriseID}
	}
	return headers, nil
}
