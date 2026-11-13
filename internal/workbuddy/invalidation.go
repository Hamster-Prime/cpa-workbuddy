package workbuddy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

type credentialState struct {
	Version          int    `json:"version"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	CredentialDigest string `json:"credential_digest"`
}

func credentialsDiffer(a, b credential) bool {
	return a.AccessToken != b.AccessToken || a.RefreshToken != b.RefreshToken || a.Generation != b.Generation
}

func credentialDigest(stored credential) (string, error) {
	raw, err := json.Marshal([2]string{stored.AccessToken, stored.RefreshToken})
	if err != nil {
		return "", failure("internal_error", "cannot identify WorkBuddy credential generation", 500)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func validCredentialState(stored credential) bool {
	state := stored.State
	if state == nil {
		return true
	}
	if state.Version != 1 || state.Status != "reauth_required" {
		return false
	}
	switch state.Reason {
	case "invalid_grant", "invalid_token", "revoked":
	default:
		return false
	}
	digest, err := credentialDigest(stored)
	return err == nil && state.CredentialDigest == digest
}

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

func reauthenticationRequired() error {
	return failure("reauth_required", "WorkBuddy credentials were revoked or rejected; sign in again", 401)
}

func (s *Service) rememberedFailure(stored credential, id string) (*credentialState, error) {
	if stored.State != nil {
		return stored.State, nil
	}
	digest, err := credentialDigest(stored)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if account := s.refreshes[id]; account != nil && account.failed != nil && account.failed.CredentialDigest == digest && account.failedGeneration == stored.Generation {
		return account.failed, nil
	}
	return nil, nil
}

// An in-memory terminal result survives a storage outage, but it never blocks a
// different host generation installed by a completed login.
func (s *Service) guardAuth(ctx context.Context, auth AuthData, callbackID string, call func(string, []byte) ([]byte, error)) (AuthData, error) {
	stored, _, _, err := credentialFromJSON(auth.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	state, err := s.rememberedFailure(stored, auth.ID)
	if err != nil {
		return AuthData{}, err
	}
	if state == nil {
		return auth, nil
	}
	current, err := hostCredential(ctx, auth, callbackID, call)
	if err != nil {
		return AuthData{}, err
	}
	latest, _, _, err := credentialFromJSON(current.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	digest, err := credentialDigest(latest)
	if err != nil {
		return AuthData{}, err
	}
	if digest != state.CredentialDigest || latest.Generation != stored.Generation {
		s.mu.Lock()
		if account := s.refreshes[auth.ID]; account != nil && account.failedGeneration == stored.Generation {
			account.failed = nil
			account.failedGeneration = ""
		}
		s.mu.Unlock()
		if latest.State != nil {
			return current, reauthenticationRequired()
		}
		return current, nil
	}
	if latest.State == nil {
		latest.State = state
		latest.ModelDiscoveryFailure = "auth_rejected"
		current, err = authFromCredential(latest, current.Metadata)
		if err != nil {
			return AuthData{}, err
		}
	}
	return current, reauthenticationRequired()
}

func (s *Service) invalidate(ctx context.Context, auth AuthData, reason, callbackID string, call func(string, []byte) ([]byte, error)) (AuthData, error) {
	current, err := hostCredential(ctx, auth, callbackID, call)
	if err != nil {
		return AuthData{}, err
	}
	old, _, _, err := credentialFromJSON(auth.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	stored, _, _, err := credentialFromJSON(current.StorageJSON)
	if err != nil {
		return AuthData{}, err
	}
	if credentialsDiffer(stored, old) {
		return current, nil
	}
	digest, err := credentialDigest(stored)
	if err != nil {
		return AuthData{}, err
	}
	stored.State = &credentialState{Version: 1, Status: "reauth_required", Reason: reason, CredentialDigest: digest}
	stored.ModelDiscoveryFailure = "auth_rejected"
	next, err := authFromCredential(stored, current.Metadata)
	if err != nil {
		return AuthData{}, err
	}
	s.mu.Lock()
	if s.refreshes == nil {
		s.refreshes = make(map[string]*refreshAccount)
	}
	account := s.refreshes[auth.ID]
	if account == nil {
		account = &refreshAccount{gate: make(chan struct{}, 1), results: make(map[rotationKey]*tokenRotation)}
		s.refreshes[auth.ID] = account
	}
	account.failed = stored.State
	account.failedGeneration = stored.Generation
	s.mu.Unlock()
	if err := saveRotation(next, current, callbackID, call); err != nil {
		latest, readErr := hostCredential(ctx, auth, callbackID, call)
		if readErr == nil {
			value, _, _, decodeErr := credentialFromJSON(latest.StorageJSON)
			if decodeErr == nil {
				if credentialsDiffer(value, old) {
					return latest, nil
				}
				if value.State != nil && value.State.CredentialDigest == digest {
					return latest, reauthenticationRequired()
				}
			}
		}
		return next, failure("credential_state_save_failed", "WorkBuddy credentials require login but their failure state could not be saved; restarting may lose the state", 502)
	}
	return next, reauthenticationRequired()
}

func (s *Service) unavailableModels(auth AuthData, callbackID string, call func(string, []byte) ([]byte, error)) (ModelResponse, error) {
	err := hostRPC(call, "host.log", struct {
		HostCallbackID string         `json:"host_callback_id"`
		Level          string         `json:"level"`
		Message        string         `json:"message"`
		Fields         map[string]any `json:"fields"`
	}{callbackID, "warn", "WorkBuddy model discovery", map[string]any{"auth_id": auth.ID, "reason": "reauth_required", "status": "unavailable", "model_count": 0}}, nil)
	if err != nil {
		return ModelResponse{}, err
	}
	return ModelResponse{Provider: provider, Models: []ModelInfo{}, AuthUpdate: auth}, nil
}

func (s *Service) recoverAuthentication(ctx context.Context, auth AuthData, reason, callbackID string, call func(string, []byte) ([]byte, error)) (AuthData, error) {
	next, err := s.renew(ctx, auth, callbackID, call)
	if err == nil {
		return next, nil
	}
	detail, ok := errors.AsType[*Error](err)
	if ok && (detail.Code == "refresh_unavailable" || detail.Code == "refresh_expired") && reason == "invalid_token" {
		return s.invalidate(ctx, auth, "invalid_token", callbackID, call)
	}
	return next, err
}

func authStatePlan(stored credential) time.Time {
	if stored.State != nil {
		return time.Time{}
	}
	return stored.ExpiresAt.Add(-refreshLead)
}

func (s *Service) persistRotation(ctx context.Context, next, expected AuthData, callbackID string, call func(string, []byte) ([]byte, error)) (AuthData, error) {
	err := saveRotation(next, expected, callbackID, call)
	if err == nil {
		return next, nil
	}
	latest, readErr := hostCredential(ctx, expected, callbackID, call)
	if readErr != nil {
		return next, errors.Join(err, readErr)
	}
	stored, _, _, decodeErr := credentialFromJSON(latest.StorageJSON)
	if decodeErr != nil {
		return next, decodeErr
	}
	if stored.State != nil {
		return latest, reauthenticationRequired()
	}
	previous, _, _, decodeErr := credentialFromJSON(expected.StorageJSON)
	if decodeErr != nil {
		return next, decodeErr
	}
	if credentialsDiffer(stored, previous) {
		return latest, nil
	}
	return next, err
}
