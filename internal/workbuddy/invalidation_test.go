package workbuddy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

func TestRefreshErrorsPreserveManagerClassificationWithoutPersistence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   any
		code   string
		want   int
	}{
		{"plain-unauthorized", 401, map[string]any{"error": "unauthorized"}, "refresh_rejected", 401},
		{"invalid-grant", 400, map[string]any{"error": "invalid_grant"}, "invalid_grant", 401},
		{"typed-invalid-grant", 400, map[string]any{"error": map[string]any{"type": "invalid_grant"}}, "invalid_grant", 401},
		{"coded-invalid-grant", 400, map[string]any{"error": map[string]any{"code": "invalid_grant"}}, "invalid_grant", 401},
		{"invalid-grant-with-nonnumeric-code", 400, map[string]any{"code": "oauth_error", "error": "invalid_grant"}, "invalid_grant", 401},
		{"revoked", 400, map[string]any{"error": "revoked"}, "invalid_grant", 401},
		{"token-revoked", 400, map[string]any{"error": "token_revoked"}, "invalid_grant", 401},
		{"typed-revoked", 400, map[string]any{"error": map[string]any{"type": "revoked"}}, "invalid_grant", 401},
		{"expired-business-code", 200, map[string]any{"code": 12153}, "refresh_rejected", 401},
		{"rejected-business-code", 200, map[string]any{"code": "11001"}, "refresh_rejected", 401},
		{"message-is-not-a-signal", 400, map[string]any{"error": map[string]any{"message": "invalid_grant revoked invalid_token"}}, "refresh_failed", 400},
		{"permission", 403, map[string]any{"error": "permission_denied"}, "refresh_failed", 403},
		{"quota", 429, map[string]any{"error": "quota_exceeded"}, "refresh_rate_limited", 429},
		{"timeout", 408, map[string]any{}, "refresh_timeout", 504},
		{"server", 502, map[string]any{"error": "invalid_grant"}, "refresh_failed", 502},
		{"network", 0, nil, "host_http_failed", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "team", time.Hour)
			h := newRotationHost(t, auth)
			before := h.raw(auth.ID)
			h.onRefresh = func(httpRequest) ([]byte, error) {
				if tc.status == 0 {
					return nil, errors.New("isolated connection failure with secret-token")
				}
				return response(tc.status, tc.body)
			}
			_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			detail, ok := errors.AsType[*workbuddy.Error](err)
			if !ok || detail.Code != tc.code || detail.HTTPStatus != tc.want {
				t.Fatalf("refresh error = %#v, want %s HTTP%d", err, tc.code, tc.want)
			}
			if bytes.Contains([]byte(detail.Message), []byte("secret-token")) || bytes.Contains([]byte(detail.Message), []byte("refresh-start")) {
				t.Fatal("safe refresh error exposed upstream details or credentials")
			}
			if h.saves != 0 || !bytes.Equal(before, h.raw(auth.ID)) {
				t.Fatal("refresh failure mutated host credential state")
			}
			if headers, err := auth.RequestHeaders(); err != nil || headers["Authorization"][0] != "Bearer access-start" {
				t.Fatal("refresh failure locally invalidated the access token")
			}
			h.onRefresh = nil
			result, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			if err != nil || result.(workbuddy.RefreshResponse).Auth.Metadata["access_token"] != "access-next" || len(h.refreshes) != 2 {
				t.Fatalf("plugin cached a failed refresh or retained terminal state: %v", err)
			}
		})
	}
}

func TestRefreshUnavailableCredentialsReturnUnauthorizedWithoutNetwork(t *testing.T) {
	for _, mode := range []string{"missing", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			want := "refresh_unavailable"
			if mode == "missing" {
				auth.Metadata["refresh_token"] = ""
			} else {
				auth.Metadata["refresh_expires_at"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
				want = "refresh_expired"
			}
			raw, err := json.Marshal(auth.Metadata)
			if err != nil {
				t.Fatal(err)
			}
			auth = parseRefreshAuth(t, raw)
			_, err = invoke(s, context.Background(), "auth.refresh", refreshWire(auth), func(method string, _ []byte) ([]byte, error) {
				t.Errorf("unavailable refresh called %s", method)
				return nil, errors.New("unexpected callback")
			})
			if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.Code != want || detail.HTTPStatus != 401 {
				t.Fatalf("unavailable refresh error = %v", err)
			}
		})
	}
}

func TestCredentialMetadataPreservesNativeRefreshScheduling(t *testing.T) {
	for _, state := range []any{
		map[string]any{"version": 1, "status": "reauth_required", "reason": "invalid_grant", "credential_digest": "legacy-digest"},
		"reauth_required",
		nil,
	} {
		auth := refreshIdentity(t, "workbuddy", "", time.Hour)
		auth.Metadata["credential_state"] = state
		raw, err := json.Marshal(auth.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		restored := parseRefreshAuth(t, raw)
		if fmt.Sprint(restored.Metadata["refresh_interval_seconds"]) != "300" || restored.ID != auth.ID {
			t.Fatal("credential metadata suppressed native refresh scheduling")
		}
		if _, present := restored.Metadata["credential_state"]; present || bytes.Contains(restored.StorageJSON, []byte("credential_state")) {
			t.Fatal("legacy permanent state survived normalized browser credentials")
		}
		s := workbuddy.New()
		h := newRotationHost(t, restored)
		_, err = invoke(s, context.Background(), "auth.refresh", refreshWire(restored), h.call)
		s.Close()
		if err != nil || len(h.refreshes) != 1 || h.saves != 0 {
			t.Fatalf("legacy state blocked upstream revalidation: %v", err)
		}
	}
}

func TestChatRejectionReturnsToManagerWithoutPluginRefresh(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			h := newRotationHost(t, auth)
			call := func(method string, raw []byte) ([]byte, error) {
				if method == "host.http.do_stream" {
					reply, err := h.call(method, raw)
					if err != nil {
						return nil, err
					}
					var envelope map[string]any
					if err := json.Unmarshal(reply, &envelope); err != nil {
						return nil, err
					}
					envelope["result"].(map[string]any)["status_code"] = status
					return json.Marshal(envelope)
				}
				if method == "host.http.stream_read" {
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": []byte(`{"error":{"message":"isolated rejection"}}`), "done": true}})
				}
				return h.call(method, raw)
			}
			_, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), call)
			if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.HTTPStatus != status {
				t.Fatalf("chat HTTP%d rejection = %v", status, err)
			}
			if len(h.opens) != 1 || len(h.refreshes) != 0 || h.saves != 0 {
				t.Fatal("plugin retried or installed credentials instead of returning the rejection")
			}
		})
	}
}
