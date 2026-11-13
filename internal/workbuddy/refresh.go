package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"time"
)

const (
	refreshLead           = 5 * time.Minute
	requestRefreshLead    = time.Minute
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

type tokenRotation struct {
	sourceAccess string
	next         credential
	at           time.Time
	saveError    error
}

type refreshAccount struct {
	gate    chan struct{}
	results map[string]*tokenRotation
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

func (s *Service) cachedAuth(auth AuthData) (AuthData, error) {
	stored, _, _, err := credentialFromJSON(auth.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	s.mu.Lock()
	var rotation *tokenRotation
	if account := s.refreshes[auth.ID]; account != nil {
		rotation = account.results[stored.RefreshToken]
		if rotation != nil && rotation.saveError == nil && time.Since(rotation.at) >= refreshResultLifetime {
			delete(account.results, stored.RefreshToken)
			rotation = nil
		}
	}
	s.mu.Unlock()
	if rotation != nil && rotation.sourceAccess == stored.AccessToken {
		return rotatedAuth(auth, rotation.next)
	}
	return auth, nil
}

func hostCredential(ctx context.Context, auth AuthData, callbackID string, call func(string, []byte) ([]byte, error)) (AuthData, error) {
	if ctx.Err() != nil {
		return AuthData{}, failure("request_cancelled", "WorkBuddy request was cancelled or expired", 408)
	}
	var listed struct {
		Files []struct {
			ID        string `json:"id"`
			AuthIndex string `json:"auth_index"`
			Provider  string `json:"provider"`
		}
	}
	if err := hostRPC(call, "host.auth.list", struct {
		HostCallbackID string `json:"host_callback_id"`
	}{callbackID}, &listed); err != nil {
		return AuthData{}, failure("credential_read_failed", "cannot read current WorkBuddy credentials from the host", 502)
	}
	for _, entry := range listed.Files {
		if entry.ID != auth.ID || entry.Provider != provider {
			continue
		}
		var current struct {
			JSON json.RawMessage `json:"json"`
		}
		err := hostRPC(call, "host.auth.get", struct {
			HostCallbackID string `json:"host_callback_id"`
			AuthIndex      string `json:"auth_index"`
		}{callbackID, entry.AuthIndex}, &current)
		if err != nil {
			return AuthData{}, failure("credential_read_failed", "cannot read current WorkBuddy credentials from the host", 502)
		}
		return checkedAuth(auth.ID, provider, current.JSON, nil)
	}
	return AuthData{}, failure("credential_unavailable", "WorkBuddy credentials are no longer installed in the host", 409)
}

func saveRotation(auth AuthData, callbackID string, call func(string, []byte) ([]byte, error)) error {
	payload, err := json.Marshal(auth.Metadata)
	if err != nil {
		return failure("credential_save_failed", "WorkBuddy credentials rotated but could not be saved; restarting may lose the renewed credentials", 502)
	}
	err = hostRPC(call, "host.auth.save", struct {
		HostCallbackID string          `json:"host_callback_id"`
		Name           string          `json:"name"`
		JSON           json.RawMessage `json:"json"`
	}{callbackID, auth.FileName, payload}, nil)
	if err != nil {
		return failure("credential_save_failed", "WorkBuddy credentials rotated but could not be saved; restarting may lose the renewed credentials", 502)
	}
	return nil
}

func refreshFailure(err error) error {
	if detail, ok := errors.AsType[*upstreamError](err); ok {
		if detail.status == 401 || detail.code == 11001 || detail.code == 12153 {
			return failure("refresh_rejected", "WorkBuddy refused the refresh token; sign in again", 401)
		}
		if detail.status == 408 {
			return failure("refresh_timeout", "WorkBuddy token refresh timed out", 504)
		}
		if detail.status == 429 {
			return failure("refresh_rate_limited", "WorkBuddy token refresh was rate limited", 429)
		}
		return failure("refresh_failed", "WorkBuddy token refresh failed", 502)
	}
	if detail, ok := errors.AsType[*Error](err); ok && detail.Code == "login_cancelled" {
		return failure("request_cancelled", "WorkBuddy token refresh was cancelled or expired", 408)
	}
	return failure("refresh_failed", "WorkBuddy token refresh failed", 502)
}

// renew serializes by stable account identity. The consumed token's result is
// installed before persistence can fail, so the next caller cannot spend it again.
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
		account = &refreshAccount{gate: make(chan struct{}, 1), results: make(map[string]*tokenRotation)}
		s.refreshes[auth.ID] = account
	}
	s.mu.Unlock()
	select {
	case account.gate <- struct{}{}:
		defer func() { <-account.gate }()
	case <-ctx.Done():
		return AuthData{}, failure("request_cancelled", "WorkBuddy token refresh was cancelled or expired", 408)
	}
	base, err := hostCredential(ctx, auth, callbackID, call)
	if err != nil {
		return AuthData{}, err
	}
	original, _, _, err := credentialFromJSON(auth.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	stored, _, _, err := credentialFromJSON(base.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	s.mu.Lock()
	for token, result := range account.results {
		if result.saveError == nil && time.Since(result.at) >= refreshResultLifetime {
			delete(account.results, token)
		}
	}
	cached := account.results[stored.RefreshToken]
	s.mu.Unlock()
	if cached != nil && (cached.sourceAccess == stored.AccessToken || cached.next.AccessToken == stored.AccessToken) {
		if cached.next.AccessToken == stored.AccessToken && cached.next.RefreshToken == stored.RefreshToken {
			return base, nil
		}
		next, err := rotatedAuth(base, cached.next)
		if err != nil {
			return AuthData{}, err
		}
		saveErr := saveRotation(next, callbackID, call)
		s.mu.Lock()
		cached.saveError = saveErr
		s.mu.Unlock()
		return next, saveErr
	}
	if stored.AccessToken != original.AccessToken || stored.RefreshToken != original.RefreshToken {
		return base, nil
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
	headers, err := base.RequestHeaders()
	if err != nil {
		return AuthData{}, err
	}
	delete(headers, "Authorization")
	headers["X-Refresh-Token"] = []string{stored.RefreshToken}
	headers["X-Auth-Refresh-Source"] = []string{"plugin"}
	var token tokenData
	err = request(ctx, chosen, callbackID, call, "POST", "/v2/plugin/auth/token/refresh", headers, []byte("{}"), &token)
	if err != nil {
		// A different host request may have installed a newer generation while this
		// refresh was running. Its success is authoritative over our stale failure.
		latest, readErr := hostCredential(ctx, auth, callbackID, call)
		if readErr != nil {
			return AuthData{}, errors.Join(refreshFailure(err), readErr)
		}
		current, _, _, decodeErr := credentialFromJSON(latest.StorageJSON)
		if decodeErr != nil {
			return AuthData{}, decodeErr
		}
		if current.AccessToken != stored.AccessToken || current.RefreshToken != stored.RefreshToken {
			return latest, nil
		}
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
	rotation := &tokenRotation{sourceAccess: stored.AccessToken, next: next, at: time.Now()}
	latest, readErr := hostCredential(ctx, auth, callbackID, call)
	if readErr == nil {
		current, _, _, decodeErr := credentialFromJSON(latest.StorageJSON)
		if decodeErr != nil {
			readErr = decodeErr
		} else if current.AccessToken != stored.AccessToken || current.RefreshToken != stored.RefreshToken {
			rotation.next = current
			base = latest
		} else {
			base = latest
		}
	}
	if readErr != nil {
		rotation.saveError = failure("credential_save_failed", "WorkBuddy credentials rotated but the current host record could not be checked; restarting may lose the renewed credentials", 502)
	} else {
		nextAuth, encodeErr := rotatedAuth(base, rotation.next)
		if encodeErr != nil {
			rotation.saveError = encodeErr
		} else {
			if rotation.next.AccessToken == next.AccessToken && rotation.next.RefreshToken == next.RefreshToken {
				rotation.saveError = saveRotation(nextAuth, callbackID, call)
			}
		}
	}
	s.mu.Lock()
	account.results[stored.RefreshToken] = rotation
	s.mu.Unlock()
	nextAuth, encodeErr := rotatedAuth(base, rotation.next)
	if encodeErr != nil {
		return AuthData{}, encodeErr
	}
	return nextAuth, rotation.saveError
}

func (s *Service) freshAuth(ctx context.Context, auth AuthData, callbackID string, call func(string, []byte) ([]byte, error)) (AuthData, bool, error) {
	current, err := s.cachedAuth(auth)
	if err != nil {
		return AuthData{}, false, err
	}
	stored, _, _, err := credentialFromJSON(current.StorageJSON)
	if err != nil {
		return AuthData{}, false, err
	}
	original, _, _, err := credentialFromJSON(auth.StorageJSON)
	if err != nil {
		return AuthData{}, false, err
	}
	changed := stored.AccessToken != original.AccessToken || stored.RefreshToken != original.RefreshToken
	if changed {
		hostAuth, err := hostCredential(ctx, auth, callbackID, call)
		if err != nil {
			return AuthData{}, false, err
		}
		hostStored, _, _, err := credentialFromJSON(hostAuth.StorageJSON)
		if err != nil {
			return AuthData{}, false, err
		}
		if hostStored.AccessToken != original.AccessToken || hostStored.RefreshToken != original.RefreshToken {
			current = hostAuth
			stored = hostStored
		} else {
			current, err = rotatedAuth(hostAuth, stored)
			if err != nil {
				return AuthData{}, false, err
			}
		}
	}
	if time.Now().Before(stored.ExpiresAt.Add(-requestRefreshLead)) {
		return current, changed, nil
	}
	next, err := s.renew(ctx, current, callbackID, call)
	if err == nil {
		return next, true, nil
	}
	detail, ok := errors.AsType[*Error](err)
	if !ok || detail.Code == "credential_save_failed" || detail.Code == "credential_unavailable" || ctx.Err() != nil || !time.Now().Before(stored.ExpiresAt) {
		return AuthData{}, false, err
	}
	logErr := hostRPC(call, "host.log", struct {
		HostCallbackID string            `json:"host_callback_id"`
		Level          string            `json:"level"`
		Message        string            `json:"message"`
		Fields         map[string]string `json:"fields"`
	}{callbackID, "warn", "WorkBuddy token refresh failed; retaining unexpired access token", map[string]string{"auth_id": auth.ID, "reason": detail.Code}}, nil)
	if logErr != nil {
		return AuthData{}, false, errors.Join(err, logErr)
	}
	return current, changed, nil
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
