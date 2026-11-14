package workbuddy

import (
	"context"
	"errors"
	"maps"
	"time"
)

const (
	refreshLead           = 5 * time.Minute
	refreshResultLifetime = time.Minute
)

type refreshRequest struct {
	AuthID         string
	AuthProvider   string
	StorageJSON    []byte
	Metadata       map[string]any
	HostCallbackID string `json:"host_callback_id"`
}

// RefreshResponse follows CPA's existing auth.refresh response contract.
type RefreshResponse struct {
	Auth             AuthData
	NextRefreshAfter time.Time
}

type rotationKey struct {
	refresh    string
	generation string
}
type tokenRotation struct {
	sourceAccess string
	next         credential
	at           time.Time
}

type refreshAccount struct {
	gate    chan struct{}
	results map[rotationKey]*tokenRotation
}

func checkedAuth(id, authProvider string, raw []byte, metadata map[string]any) (AuthData, error) {
	stored, fields, handled, err := credentialFromJSON(raw)
	if err != nil {
		return AuthData{}, err
	}
	if authProvider != provider || !handled {
		return AuthData{}, failure("invalid_credentials", "refresh requires WorkBuddy browser credentials", 400)
	}
	maps.Copy(fields, metadata)
	auth, err := authFromCredential(stored, fields)
	if err != nil {
		return AuthData{}, err
	}
	if id != auth.ID {
		return AuthData{}, failure("identity_mismatch", "credential scope does not match the selected account", 400)
	}
	return auth, nil
}

func mergeCredentialTokens(base, next credential) credential {
	base.AccessToken = next.AccessToken
	base.RefreshToken = next.RefreshToken
	base.Domain = next.Domain
	base.TokenType = next.TokenType
	base.ExpiresAt = next.ExpiresAt
	base.RefreshExpiresAt = next.RefreshExpiresAt
	base.Generation = next.Generation
	if base.ModelSnapshot == nil || next.ModelSnapshot != nil && !base.ModelSnapshot.CapturedAt.After(next.ModelSnapshot.CapturedAt) {
		base.ModelSnapshot = next.ModelSnapshot
		base.ModelDiscoveryFailure = next.ModelDiscoveryFailure
	}
	return base
}

func rotatedAuth(base AuthData, next credential) (AuthData, error) {
	stored, metadata, _, err := credentialFromJSON(base.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	maps.Copy(metadata, base.Metadata)
	return authFromCredential(mergeCredentialTokens(stored, next), metadata)
}

func refreshFailure(err error) error {
	if detail, ok := errors.AsType[*upstreamError](err); ok {
		if detail.authReason == "invalid_grant" || detail.authReason == "revoked" {
			return failure("invalid_grant", "WorkBuddy refused the refresh grant; sign in again", 401)
		}
		if detail.status == 401 || detail.code == 11001 || detail.code == 12153 {
			return failure("refresh_rejected", "WorkBuddy refused the refresh token; sign in again", 401)
		}
		if detail.status == 408 {
			return failure("refresh_timeout", "WorkBuddy token refresh timed out", 504)
		}
		if detail.status == 429 {
			return failure("refresh_rate_limited", "WorkBuddy token refresh was rate limited", 429)
		}
		if detail.status >= 400 && detail.status < 500 {
			return failure("refresh_failed", "WorkBuddy token refresh was refused", detail.status)
		}
		return failure("refresh_failed", "WorkBuddy token refresh failed", 502)
	}
	if detail, ok := errors.AsType[*Error](err); ok {
		if detail.Code == "login_cancelled" {
			return failure("request_cancelled", "WorkBuddy token refresh was cancelled or expired", 408)
		}
		return detail
	}
	return failure("refresh_failed", "WorkBuddy token refresh failed", 502)
}

// renew shares a short-lived upstream result only within the supplied account
// and login generation. The host Manager owns installation and persistence.
func (s *Service) renew(ctx context.Context, auth AuthData, callbackID string, call func(string, []byte) ([]byte, error)) (AuthData, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return AuthData{}, failure("service_closed", "WorkBuddy service is closed", 503)
	}
	if s.refreshes == nil {
		s.refreshes = make(map[string]*refreshAccount)
	}
	account := s.refreshes[auth.ID]
	if account == nil {
		account = &refreshAccount{gate: make(chan struct{}, 1), results: make(map[rotationKey]*tokenRotation)}
		s.refreshes[auth.ID] = account
	}
	s.mu.Unlock()
	select {
	case account.gate <- struct{}{}:
		defer func() { <-account.gate }()
	case <-ctx.Done():
		return AuthData{}, failure("request_cancelled", "WorkBuddy token refresh was cancelled or expired", 408)
	}
	if ctx.Err() != nil {
		return AuthData{}, failure("request_cancelled", "WorkBuddy token refresh was cancelled or expired", 408)
	}
	stored, _, _, err := credentialFromJSON(auth.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	for key, result := range account.results {
		if time.Since(result.at) >= refreshResultLifetime {
			delete(account.results, key)
		}
	}
	key := rotationKey{stored.RefreshToken, stored.Generation}
	if cached := account.results[key]; cached != nil &&
		(cached.sourceAccess == stored.AccessToken || (cached.next.AccessToken == stored.AccessToken && cached.next.RefreshToken == stored.RefreshToken)) {
		return rotatedAuth(auth, cached.next)
	}
	if stored.RefreshToken == "" {
		return AuthData{}, failure("refresh_unavailable", "WorkBuddy credentials have no refresh token; sign in again", 401)
	}
	if stored.RefreshExpiresAt != nil && !time.Now().Before(*stored.RefreshExpiresAt) {
		return AuthData{}, failure("refresh_expired", "WorkBuddy refresh token has expired; sign in again", 401)
	}
	chosen, err := credentialSite(stored)
	if err != nil {
		return AuthData{}, err
	}
	headers, err := auth.RequestHeaders()
	if err != nil {
		return AuthData{}, err
	}
	delete(headers, "Authorization")
	headers["X-Refresh-Token"] = []string{stored.RefreshToken}
	headers["X-Auth-Refresh-Source"] = []string{"plugin"}
	var token tokenData
	if err := request(ctx, chosen, callbackID, call, "POST", "/v2/plugin/auth/token/refresh", headers, []byte("{}"), &token); err != nil {
		return AuthData{}, refreshFailure(err)
	}
	if !token.valid() {
		return AuthData{}, failure("invalid_refresh_response", "WorkBuddy returned invalid renewed credentials", 502)
	}
	token.resolveExpiry(time.Now())
	next := stored
	next.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		next.RefreshToken = token.RefreshToken
	}
	if token.Domain != "" {
		next.Domain = token.Domain
	}
	if token.TokenType != "" {
		next.TokenType = token.TokenType
	}
	next.ExpiresAt = time.UnixMilli(token.ExpiresAt).UTC()
	if token.RefreshExpiresAt > 0 {
		expiry := time.UnixMilli(token.RefreshExpiresAt).UTC()
		next.RefreshExpiresAt = &expiry
	}
	nextAuth, err := rotatedAuth(auth, next)
	if err != nil {
		return AuthData{}, err
	}
	nextAuth = captureModelSnapshot(ctx, nextAuth, callbackID, call)
	next, _, _, err = credentialFromJSON(nextAuth.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	account.results[key] = &tokenRotation{sourceAccess: stored.AccessToken, next: next, at: time.Now()}
	return nextAuth, nil
}

func (s *Service) refresh(ctx context.Context, raw []byte, call func(string, []byte) ([]byte, error)) (RefreshResponse, error) {
	var req refreshRequest
	if err := decode(raw, &req); err != nil {
		return RefreshResponse{}, err
	}
	auth, err := checkedAuth(req.AuthID, req.AuthProvider, req.StorageJSON, req.Metadata)
	if err != nil {
		return RefreshResponse{}, err
	}
	next, err := s.renew(ctx, auth, req.HostCallbackID, call)
	if err != nil {
		return RefreshResponse{}, err
	}
	return RefreshResponse{Auth: next, NextRefreshAfter: next.NextRefreshAfter}, nil
}
