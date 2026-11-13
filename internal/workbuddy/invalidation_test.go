package workbuddy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

const (
	startingCredentialDigest = "f88323bb30914dca9a75d2508486efc229e230502b66e928a912ebf22bd73d51"
	renewedCredentialDigest  = "372d8b849b79fc44f53bfa0355779ef9e4ed81b7eaa3da4779fd3710bb06d560"
)

func requireReauthentication(t *testing.T, err error) {
	t.Helper()
	if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.Code != "reauth_required" || detail.HTTPStatus != 401 {
		t.Fatalf("confirmed credential rejection = %v, want reauth_required HTTP401", err)
	}
}

func requireCredentialState(t *testing.T, auth workbuddy.AuthData, reason, digest string) {
	t.Helper()
	state, ok := auth.Metadata["credential_state"].(map[string]any)
	if !ok {
		t.Fatalf("credential_state = %#v, want persisted state object", auth.Metadata["credential_state"])
	}
	if len(state) != 4 || state["version"] != float64(1) || state["status"] != "reauth_required" || state["reason"] != reason || state["credential_digest"] != digest {
		t.Fatalf("credential_state = %#v", state)
	}
	if !auth.NextRefreshAfter.IsZero() {
		t.Fatal("permanently rejected credential retained a refresh schedule")
	}
}

func requireHealthyCredential(t *testing.T, raw []byte) {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["credential_state"] != nil {
		t.Fatalf("ordinary failure persisted credential_state: %#v", fields["credential_state"])
	}
}

// The wrapper changes only public HTTP replies; rotationHost continues to own
// host persistence, credential replacement, and request accounting.
func invalidationHTTPHost(h *rotationHost, reject func(httpRequest) (int, any, string)) hostCall {
	return func(method string, raw []byte) ([]byte, error) {
		if method == "host.http.do" || method == "host.http.do_stream" {
			var req httpRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
			status, body, challenge := reject(req)
			if status != 0 {
				reply, err := h.call(method, raw)
				if err != nil {
					return nil, err
				}
				headers := map[string][]string{}
				if challenge != "" {
					headers["Www-Authenticate"] = []string{challenge}
				}
				if method == "host.http.do" {
					payload, err := json.Marshal(body)
					if err != nil {
						return nil, err
					}
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"StatusCode": status, "Headers": headers, "Body": payload}})
				}
				var envelope struct {
					Result struct {
						StreamID string `json:"stream_id"`
					} `json:"result"`
				}
				if err := json.Unmarshal(reply, &envelope); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": status, "headers": headers, "stream_id": envelope.Result.StreamID}})
			}
		}
		if method == "host.http.stream_read" {
			payload := []byte(`{"error":{"message":"isolated upstream rejection"}}`)
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": payload, "done": true}})
		}
		return h.call(method, raw)
	}
}

func TestRefreshExplicitPermanentFailuresPersistCredentialState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   any
		reason string
	}{
		{"invalid-grant", map[string]any{"error": "invalid_grant"}, "invalid_grant"},
		{"typed-invalid-grant", map[string]any{"error": map[string]any{"type": "invalid_grant"}}, "invalid_grant"},
		{"revoked", map[string]any{"error": "revoked"}, "revoked"},
		{"typed-revoked", map[string]any{"error": map[string]any{"type": "revoked"}}, "revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "team", time.Hour)
			h := newRotationHost(t, auth)
			h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, tc.body) }
			_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			requireReauthentication(t, err)
			stored := parseRefreshAuth(t, h.raw(auth.ID))
			requireCredentialState(t, stored, tc.reason, startingCredentialDigest)
			if stored.ID != auth.ID || !stored.Disabled || stored.Metadata["disabled"] != true || stored.Metadata["priority"] != float64(7) || stored.ProxyURL != auth.ProxyURL {
				t.Fatal("invalidation changed operator management metadata or identity")
			}
			if stored.Metadata["access_token"] != "access-start" || stored.Metadata["refresh_token"] != "refresh-start" || h.saves != 1 {
				t.Fatal("invalidation changed credential tokens or failed to save once")
			}
			for _, entry := range h.logs {
				if bytes.Contains(entry, []byte("access-start")) || bytes.Contains(entry, []byte("refresh-start")) {
					t.Fatal("invalidation diagnostic exposed credential tokens")
				}
			}
		})
	}
}

func TestRefreshOrdinaryFailuresDoNotInvalidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   any
	}{
		{"plain-401", 401, map[string]any{"error": "unauthorized"}},
		{"message-is-not-a-signal", 401, map[string]any{"error": map[string]any{"message": "invalid_grant revoked invalid_token"}}},
		{"permission", 403, map[string]any{"error": "permission_denied"}},
		{"quota", 429, map[string]any{"error": "quota_exceeded"}},
		{"server", 502, map[string]any{"error": "bad_gateway"}},
		{"network", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			h := newRotationHost(t, auth)
			before := h.raw(auth.ID)
			h.onRefresh = func(httpRequest) ([]byte, error) {
				if tc.status == 0 {
					return nil, errors.New("isolated connection failure")
				}
				return response(tc.status, tc.body)
			}
			_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			if err == nil {
				t.Fatal("failed refresh succeeded")
			}
			if detail, ok := errors.AsType[*workbuddy.Error](err); ok && detail.Code == "reauth_required" {
				t.Fatalf("ordinary refresh failure became permanent: %v", err)
			}
			if h.saves != 0 || !bytes.Equal(before, h.raw(auth.ID)) {
				t.Fatal("ordinary refresh failure changed persisted credentials")
			}
			requireHealthyCredential(t, h.raw(auth.ID))
			if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
				t.Fatalf("ordinary refresh failure blocked healthy access: %v", err)
			}
		})
	}
}

func TestChatOrdinaryRejectionsDoNotInvalidate(t *testing.T) {
	for _, status := range []int{401, 403, 429, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			h := newRotationHost(t, auth)
			call := invalidationHTTPHost(h, func(req httpRequest) (int, any, string) {
				if strings.HasSuffix(req.URL, "/v2/plugin/auth/token/refresh") {
					return 0, nil, ""
				}
				return status, map[string]any{"error": map[string]any{"message": "invalid_token revoked"}}, ""
			})
			_, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), call)
			if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.HTTPStatus != status || detail.Code == "reauth_required" {
				t.Fatalf("ordinary HTTP%d rejection = %v", status, err)
			}
			requireHealthyCredential(t, h.raw(auth.ID))
			wantRefreshes := 0
			if status == 401 {
				wantRefreshes = 1
			}
			if len(h.refreshes) != wantRefreshes || h.saves != wantRefreshes {
				t.Fatal("ordinary rejection performed permanent-state persistence or unbounded recovery")
			}
		})
	}
}

func TestChatRenewedInvalidTokenPersistsCurrentGeneration(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy-ai", "", time.Hour)
	h := newRotationHost(t, auth)
	call := invalidationHTTPHost(h, func(req httpRequest) (int, any, string) {
		if strings.HasSuffix(req.URL, "/v2/plugin/auth/token/refresh") {
			return 0, nil, ""
		}
		return 401, map[string]any{"error": "unauthorized"}, `Bearer error="invalid_token"`
	})
	_, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), call)
	requireReauthentication(t, err)
	if len(h.refreshes) != 1 || len(h.opens) != 2 || h.saves != 2 {
		t.Fatalf("recovery refreshes=%d opens=%d saves=%d", len(h.refreshes), len(h.opens), h.saves)
	}
	requireCredentialState(t, parseRefreshAuth(t, h.raw(auth.ID)), "invalid_token", renewedCredentialDigest)
	if strings.Join(h.opens[0].Headers["Authorization"], "") != "Bearer access-start" || strings.Join(h.opens[1].Headers["Authorization"], "") != "Bearer access-next" {
		t.Fatal("terminal invalid_token was not confirmed against renewed credentials")
	}
	if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), call); err == nil {
		t.Fatal("confirmed rejection was forgotten on the next request")
	} else {
		requireReauthentication(t, err)
	}
	if len(h.opens) != 2 || len(h.refreshes) != 1 || h.saves != 2 {
		t.Fatal("confirmed credential state performed more network or persistence work")
	}
}

func TestInitialInvalidTokenCanRecoverWithoutPermanentState(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	challenge := invalidationHTTPHost(h, func(req httpRequest) (int, any, string) {
		if strings.Join(req.Headers["Authorization"], "") == "Bearer access-start" {
			return 401, map[string]any{"error": "unauthorized"}, `Bearer error="invalid_token"`
		}
		return 0, nil, ""
	})
	call := func(method string, raw []byte) ([]byte, error) {
		if method == "host.http.stream_read" {
			var req struct {
				StreamID string `json:"stream_id"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
			if req.StreamID == "rotation-stream-2" {
				return h.call(method, raw)
			}
		}
		return challenge(method, raw)
	}
	if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), call); err != nil {
		t.Fatal(err)
	}
	if len(h.refreshes) != 1 || len(h.opens) != 2 || h.saves != 1 {
		t.Fatal("initial invalid_token did not get exactly one recovery")
	}
	requireHealthyCredential(t, h.raw(auth.ID))
}

func TestPermanentCredentialStateRestoresAcrossServiceRestart(t *testing.T) {
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
	s := workbuddy.New()
	_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
	requireReauthentication(t, err)
	s.Close()
	restored := parseRefreshAuth(t, h.raw(auth.ID))
	requireCredentialState(t, restored, "invalid_grant", startingCredentialDigest)
	restarted := workbuddy.New()
	defer restarted.Close()
	for _, method := range []string{"auth.refresh", "executor.execute"} {
		input := refreshWire(restored)
		if method == "executor.execute" {
			input = chatWireRequest(restored, false)
		}
		_, err := invoke(restarted, context.Background(), method, input, h.call)
		requireReauthentication(t, err)
	}
	result, err := invoke(restarted, context.Background(), "model.for_auth", refreshWire(restored), h.call)
	if err != nil {
		t.Fatal(err)
	}
	models := result.(workbuddy.ModelResponse)
	if len(models.Models) != 0 {
		t.Fatal("restarted invalid credential exposed models")
	}
	requireCredentialState(t, models.AuthUpdate, "invalid_grant", startingCredentialDigest)
	if len(h.refreshes) != 1 || len(h.opens) != 0 || len(h.configTokens) != 0 || h.saves != 1 {
		t.Fatal("restart consumed or resaved a permanently rejected credential")
	}
}

func TestModelDiscoveryPermanentFailureReturnsEmptyModelsAndState(t *testing.T) {
	for _, mode := range []string{"refresh-invalid-grant", "renewed-invalid-token"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			remaining := time.Hour
			if mode == "refresh-invalid-grant" {
				remaining = 30 * time.Second
			}
			auth := refreshIdentity(t, "workbuddy", "", remaining)
			h := newRotationHost(t, auth)
			call := hostCall(h.call)
			reason, digest := "invalid_grant", startingCredentialDigest
			if mode == "refresh-invalid-grant" {
				h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
			} else {
				reason, digest = "invalid_token", renewedCredentialDigest
				call = invalidationHTTPHost(h, func(req httpRequest) (int, any, string) {
					if strings.HasSuffix(req.URL, "/v3/config") {
						return 401, map[string]any{"error": "unauthorized"}, `Bearer error="invalid_token"`
					}
					return 0, nil, ""
				})
			}
			result, err := invoke(s, context.Background(), "model.for_auth", refreshWire(auth), call)
			if err != nil {
				t.Fatal(err)
			}
			got := result.(workbuddy.ModelResponse)
			if got.Provider != "workbuddy" || len(got.Models) != 0 {
				t.Fatal("permanent model discovery rejection did not return empty models")
			}
			requireCredentialState(t, got.AuthUpdate, reason, digest)
			requireCredentialState(t, parseRefreshAuth(t, h.raw(auth.ID)), reason, digest)
			if len(h.refreshes) != 1 {
				t.Fatal("model discovery exceeded bounded recovery")
			}
			wantConfigs := 0
			if mode == "renewed-invalid-token" {
				wantConfigs = 2
			}
			if len(h.configTokens) != wantConfigs {
				t.Fatal("model discovery contacted config after known permanent refresh failure or skipped recovery")
			}
		})
	}
}

func TestCredentialStateSaveFailureRetainsMemoryRejection(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	before := h.raw(auth.ID)
	h.failSave = true
	h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
	_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
	if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.Code != "credential_state_save_failed" {
		t.Fatalf("invalidation persistence failure = %v", err)
	}
	if !bytes.Equal(before, h.raw(auth.ID)) || h.saves != 1 {
		t.Fatal("failed state save changed host credentials")
	}
	_, err = invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call)
	requireReauthentication(t, err)
	if len(h.refreshes) != 1 || len(h.opens) != 0 {
		t.Fatal("failed state save reconsumed rejected credentials or sent them upstream")
	}
}

func TestPermanentCacheAdoptsExternallyReplacedCredentials(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
	_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
	requireReauthentication(t, err)
	if err := h.replace(auth.ID, "access-theirs", "refresh-theirs"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
		t.Fatalf("cached invalidation overrode externally replaced credentials: %v", err)
	}
	if len(h.opens) != 1 || strings.Join(h.opens[0].Headers["Authorization"], "") != "Bearer access-theirs" || len(h.refreshes) != 1 || h.saves != 1 {
		t.Fatal("external credential generation was not adopted without consuming or overwriting it")
	}
}

func TestInflightPermanentFailureCannotMarkReplacementCredentials(t *testing.T) {
	for _, stage := range []string{"upstream-inflight", "host-save-cas"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := workbuddy.New()
				defer s.Close()
				auth := refreshIdentity(t, "workbuddy", "", time.Hour)
				h := newRotationHost(t, auth)
				release := make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				defer unblock()
				h.onRefresh = func(httpRequest) ([]byte, error) {
					if stage == "upstream-inflight" {
						<-release
					}
					return response(400, map[string]any{"error": "invalid_grant"})
				}
				call := func(method string, raw []byte) ([]byte, error) {
					if stage == "host-save-cas" && method == "host.auth.save" {
						var req struct {
							Expected json.RawMessage `json:"expected_credential_json"`
						}
						if err := json.Unmarshal(raw, &req); err != nil {
							return nil, err
						}
						var expected map[string]any
						if err := json.Unmarshal(req.Expected, &expected); err != nil {
							t.Error("permanent state save omitted its expected credential generation")
							return nil, err
						}
						if expected["access_token"] != "access-start" || expected["refresh_token"] != "refresh-start" {
							t.Error("state-save CAS was not bound to rejected credentials")
						}
						if err := h.replace(auth.ID, "access-theirs", "refresh-theirs"); err != nil {
							return nil, err
						}
					}
					return h.call(method, raw)
				}
				type outcome struct {
					value any
					err   error
				}
				result := make(chan outcome, 1)
				go func() {
					value, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), call)
					result <- outcome{value, err}
				}()
				if stage == "upstream-inflight" {
					synctest.Wait()
					if len(h.refreshes) != 1 {
						t.Fatal("request did not reach the intended inflight refresh boundary")
					}
					if err := h.replace(auth.ID, "access-theirs", "refresh-theirs"); err != nil {
						t.Fatal(err)
					}
					unblock()
				}
				got := <-result
				if got.err != nil {
					t.Fatalf("stale permanent failure was not replaced with the host generation: %v", got.err)
				}
				if got.value.(workbuddy.RefreshResponse).Auth.Metadata["access_token"] != "access-theirs" {
					t.Fatal("stale permanent failure returned rejected credentials")
				}
				stored := parseRefreshAuth(t, h.raw(auth.ID))
				if stored.Metadata["access_token"] != "access-theirs" || stored.Metadata["refresh_token"] != "refresh-theirs" {
					t.Fatal("stale invalidation overwrote externally saved credentials")
				}
				requireHealthyCredential(t, h.raw(auth.ID))
				if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
					t.Fatalf("stale invalidation poisoned adopted credentials: %v", err)
				}
			})
		})
	}
}

func TestHealthyBrowserLoginExplicitlyClearsCredentialState(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
	_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
	requireReauthentication(t, err)
	call := host(t, func(req httpRequest) ([]byte, error) {
		switch {
		case strings.Contains(req.URL, "/v2/plugin/auth/state"):
			return success(stateReply("workbuddy"))
		case strings.Contains(req.URL, "/v2/plugin/auth/token"):
			return success(tokenReply())
		case strings.Contains(req.URL, "/v2/plugin/login/account"):
			return success(map[string]any{"uid": "person", "nickname": "Person", "type": "personal"})
		default:
			return nil, errors.New("unexpected login endpoint")
		}
	})
	flow := begin(t, s, "workbuddy", call)
	login := poll(t, s, context.Background(), flow.State, call)
	if login.Status != "success" || login.Auth.ID != auth.ID {
		t.Fatal("healthy browser relogin did not restore the same credential identity")
	}
	if generation, ok := login.Auth.Metadata["credential_generation"].(string); !ok || generation == "" {
		t.Fatal("completed login omitted its credential generation")
	}
	state, present := login.Auth.Metadata["credential_state"]
	if !present || state != nil {
		t.Fatalf("healthy login metadata credential_state = %#v, present=%v; want explicit null", state, present)
	}
	var storage map[string]any
	if err := json.Unmarshal(login.Auth.StorageJSON, &storage); err != nil {
		t.Fatal(err)
	}
	if state, present := storage["credential_state"]; !present || state != nil {
		t.Fatal("healthy browser storage did not carry explicit credential_state:null")
	}
	stored := parseRefreshAuth(t, login.Auth.StorageJSON)
	if stored.ID != login.Auth.ID || stored.Metadata["access_token"] != "browser-access" {
		t.Fatal("healthy login parse changed the credential identity or tokens")
	}
	if stored.Metadata["credential_generation"] != login.Auth.Metadata["credential_generation"] {
		t.Fatal("credential parsing changed a completed login generation")
	}
	if state, present := stored.Metadata["credential_state"]; !present || state != nil {
		t.Fatal("auth.parse dropped the healthy login's explicit state reset")
	}
	var merged map[string]any
	if err := json.Unmarshal(h.raw(auth.ID), &merged); err != nil {
		t.Fatal(err)
	}
	maps.Copy(merged, login.Auth.Metadata)
	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	saveRequest, err := json.Marshal(map[string]any{"name": auth.FileName, "json": json.RawMessage(mergedJSON)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.call("host.auth.save", saveRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
		t.Fatalf("relogin did not clear cached permanent state: %v", err)
	}
	if len(h.opens) != 1 || strings.Join(h.opens[0].Headers["Authorization"], "") != "Bearer browser-access" || len(h.refreshes) != 1 {
		t.Fatal("relogin recovery failed to adopt browser credentials without another refresh")
	}
	updated := parseRefreshAuth(t, h.raw(auth.ID))
	if !updated.Disabled || updated.Metadata["priority"] != float64(7) || updated.ProxyURL != auth.ProxyURL {
		t.Fatal("healthy relogin reset operator management metadata")
	}
}

func TestPermanentInvalidationPreservesOperatorDisabledFlag(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			auth.Metadata["disabled"] = disabled
			raw, err := json.Marshal(auth.Metadata)
			if err != nil {
				t.Fatal(err)
			}
			auth = parseRefreshAuth(t, raw)
			h := newRotationHost(t, auth)
			h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
			_, err = invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			requireReauthentication(t, err)
			stored := parseRefreshAuth(t, h.raw(auth.ID))
			if stored.Disabled != disabled || stored.Metadata["disabled"] != disabled || stored.Metadata["priority"] != float64(7) || stored.ProxyURL != auth.ProxyURL {
				t.Fatal("permanent invalidation changed the operator's disabled flag or management settings")
			}
		})
	}
}

func TestCredentialStateRequiresValidCurrentTokenBinding(t *testing.T) {
	for _, mode := range []string{"valid", "version", "status", "reason", "digest", "digest-case", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			state := map[string]any{"version": 1, "status": "reauth_required", "reason": "invalid_grant", "credential_digest": startingCredentialDigest}
			switch mode {
			case "version":
				state["version"] = 2
			case "status":
				state["status"] = "disabled"
			case "reason":
				state["reason"] = "permission_denied"
			case "digest":
				state["credential_digest"] = renewedCredentialDigest
			case "digest-case":
				state["credential_digest"] = strings.ToUpper(startingCredentialDigest)
			}
			auth.Metadata["credential_state"] = state
			if mode == "malformed" {
				auth.Metadata["credential_state"] = "reauth_required"
			}
			raw, err := json.Marshal(auth.Metadata)
			if err != nil {
				t.Fatal(err)
			}
			auth = parseRefreshAuth(t, raw)
			h := newRotationHost(t, auth)
			_, err = invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call)
			if mode == "valid" {
				requireReauthentication(t, err)
				if len(h.opens) != 0 || !auth.NextRefreshAfter.IsZero() {
					t.Fatal("valid persisted marker did not block use and refresh scheduling")
				}
			} else {
				if err != nil {
					t.Fatalf("invalid or stale marker blocked current credentials: %v", err)
				}
				if len(h.opens) != 1 || auth.NextRefreshAfter.IsZero() {
					t.Fatal("invalid marker affected healthy credential availability")
				}
			}
			if len(h.refreshes) != 0 || h.saves != 0 {
				t.Fatal("restored credential-state validation consumed tokens or rewrote storage")
			}
		})
	}
}

func TestRotationCASAdoptsLoginAndPreservesConcurrentTerminalState(t *testing.T) {
	for _, mode := range []string{"login", "terminal"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			h := newRotationHost(t, auth)
			once := false
			call := func(method string, raw []byte) ([]byte, error) {
				if method == "host.auth.save" && !once {
					once = true
					if mode == "login" {
						if err := h.replace(auth.ID, "access-theirs", "refresh-theirs"); err != nil {
							return nil, err
						}
					} else {
						var fields map[string]any
						if err := json.Unmarshal(h.raw(auth.ID), &fields); err != nil {
							return nil, err
						}
						fields["credential_state"] = map[string]any{"version": 1, "status": "reauth_required", "reason": "invalid_grant", "credential_digest": startingCredentialDigest}
						updated, err := json.Marshal(fields)
						if err != nil {
							return nil, err
						}
						h.mu.Lock()
						h.files[auth.ID] = updated
						h.mu.Unlock()
					}
				}
				return h.call(method, raw)
			}
			got, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), call)
			if mode == "terminal" {
				requireReauthentication(t, err)
				requireCredentialState(t, parseRefreshAuth(t, h.raw(auth.ID)), "invalid_grant", startingCredentialDigest)
				_, err = invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call)
				requireReauthentication(t, err)
				if len(h.opens) != 0 {
					t.Fatal("stale successful rotation cleared concurrent terminal state")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got.(workbuddy.RefreshResponse).Auth.Metadata["access_token"] != "access-theirs" {
					t.Fatal("rotation CAS did not adopt completed login")
				}
				if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
					t.Fatal(err)
				}
				if strings.Join(h.opens[0].Headers["Authorization"], "") != "Bearer access-theirs" {
					t.Fatal("stale rotation cache overwrote completed login")
				}
			}
			if len(h.refreshes) != 1 {
				t.Fatal("CAS conflict consumed refresh token twice")
			}
		})
	}
}

func TestPermanentStateRequiresAdvertisedHostContract(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	call := func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.list" {
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"files": []map[string]string{{"id": auth.ID, "auth_index": auth.ID, "provider": "workbuddy"}}}})
		}
		return h.call(method, raw)
	}
	_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), call)
	if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.Code != "unsupported_host_contract" || detail.HTTPStatus != 503 {
		t.Fatalf("unverified host contract was used: %v", err)
	}
	if len(h.refreshes) != 0 || h.saves != 0 {
		t.Fatal("host contract negotiation consumed or persisted credentials")
	}
}

func TestInterruptedLoginRetainsPermanentCredentialState(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := workbuddy.New()
				defer s.Close()
				auth := refreshIdentity(t, "workbuddy", "", time.Hour)
				h := newRotationHost(t, auth)
				h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
				_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
				requireReauthentication(t, err)
				before := h.raw(auth.ID)
				loginHost := host(t, func(req httpRequest) ([]byte, error) {
					if strings.Contains(req.URL, "/v2/plugin/auth/state") {
						return success(stateReply("workbuddy"))
					}
					return response(200, map[string]any{"code": 11217})
				})
				flow := begin(t, s, "workbuddy", loginHost)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "cancel" {
					cancel()
				} else {
					time.Sleep(5*time.Minute + time.Second)
				}
				got, pollErr := invoke(s, ctx, "auth.login.poll", map[string]any{"Provider": "workbuddy", "State": flow.State}, loginHost)
				if pollErr == nil && got.(workbuddy.PollResponse).Status == "success" {
					t.Fatal("interrupted login reported success")
				}
				if !bytes.Equal(before, h.raw(auth.ID)) {
					t.Fatal("interrupted login modified the rejected credential file")
				}
				_, err = invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call)
				requireReauthentication(t, err)
				if len(h.refreshes) != 1 || len(h.opens) != 0 {
					t.Fatal("interrupted login cleared rejected generation")
				}
			})
		})
	}
}

func TestSameTokenLoginClearsCachedRejection(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprint(persisted), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			h := newRotationHost(t, auth)
			h.failSave = !persisted
			h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
			_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			if persisted {
				requireReauthentication(t, err)
			} else if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.Code != "credential_state_save_failed" {
				t.Fatal(err)
			}
			h.failSave = false
			if err := h.replace(auth.ID, "access-start", "refresh-start"); err != nil {
				t.Fatal(err)
			}
			if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
				t.Fatal(err)
			}
			if len(h.opens) != 1 || len(h.refreshes) != 1 {
				t.Fatal("same-token completed login retained stale rejection")
			}
		})
	}
}

func TestLostTerminalSaveAcknowledgmentAdoptsPersistedState(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
	call := func(method string, raw []byte) ([]byte, error) {
		reply, err := h.call(method, raw)
		if method == "host.auth.save" && err == nil {
			return nil, errors.New("isolated lost save acknowledgement")
		}
		return reply, err
	}
	_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), call)
	requireReauthentication(t, err)
	requireCredentialState(t, parseRefreshAuth(t, h.raw(auth.ID)), "invalid_grant", startingCredentialDigest)
	_, err = invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call)
	requireReauthentication(t, err)
	if len(h.refreshes) != 1 || len(h.opens) != 0 {
		t.Fatal("lost acknowledgement cleared terminal state or reconsumed refresh token")
	}
}

func TestAdoptedRejectedGenerationRemainsBlocked(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	h.onRefresh = func(httpRequest) ([]byte, error) { return response(400, map[string]any{"error": "invalid_grant"}) }
	_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
	requireReauthentication(t, err)
	if err := h.replace(auth.ID, "access-start", "refresh-start"); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(h.raw(auth.ID), &fields); err != nil {
		t.Fatal(err)
	}
	fields["credential_state"] = map[string]any{"version": 1, "status": "reauth_required", "reason": "invalid_grant", "credential_digest": startingCredentialDigest}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.files[auth.ID] = raw
	h.mu.Unlock()
	_, err = invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call)
	requireReauthentication(t, err)
	if len(h.opens) != 0 || len(h.refreshes) != 1 {
		t.Fatal("adopting already-rejected login generation contacted upstream")
	}
}
