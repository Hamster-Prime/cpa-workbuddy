package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"cpa-workbuddy/internal/workbuddy"
)

func discover(t *testing.T, s *workbuddy.Service, auth workbuddy.AuthData, mode string) (workbuddy.ModelResponse, map[string]any) {
	t.Helper()
	var diagnostic struct {
		Message    string         `json:"message"`
		Fields     map[string]any `json:"fields"`
		CallbackID string         `json:"host_callback_id"`
	}
	result, err := invoke(s, context.Background(), "model.for_auth", map[string]any{
		"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON,
		"Metadata": auth.Metadata, "host_callback_id": "model-callback",
	}, func(method string, raw []byte) ([]byte, error) {
		if method == "host.log" {
			if err := json.Unmarshal(raw, &diagnostic); err != nil {
				return nil, err
			}
			if diagnostic.Message != "WorkBuddy model discovery" || diagnostic.CallbackID != "model-callback" || strings.Contains(string(raw), "fixture-browser-access") {
				t.Error("invalid or sensitive discovery diagnostic")
			}
			return []byte(`{"ok":true,"result":{}}`), nil
		}
		if method == "host.auth.list" {
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"credential_state_version": 1, "files": []map[string]string{{"id": auth.ID, "provider": "workbuddy", "auth_index": "discovery-current"}}}})
		}
		if method == "host.auth.get" {
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"json": json.RawMessage(auth.StorageJSON)}})
		}
		if method != "host.http.do" {
			t.Fatalf("unexpected callback %s", method)
		}
		var req httpRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		if req.Method != "GET" || !strings.HasSuffix(req.URL, "/v3/config") || req.CallbackID != "model-callback" || req.Headers["Authorization"][0] != "Bearer fixture-browser-access" || req.Headers["X-Requested-With"][0] != "XMLHttpRequest" {
			t.Fatal("configuration request lost host context or browser identity")
		}
		for key, value := range map[string]string{
			"X-Product": "SaaS", "X-IDE-Type": "WorkBuddy", "X-Agent-Intent": "craft",
			"X-Agent-Purpose": "conversation", "X-IDE-Name": "WorkBuddy", "X-IDE-Version": "5.9.3",
		} {
			if values := req.Headers[key]; len(values) != 1 || values[0] != value {
				t.Fatalf("configuration client header %s is invalid", key)
			}
		}
		if mode == "network" {
			return nil, errors.New("private network details")
		}
		if mode == "rejected" || mode == "bare401" {
			headers := map[string][]string{}
			if mode == "rejected" {
				headers["WWW-Authenticate"] = []string{`Bearer error="invalid_token"`}
			}
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"StatusCode": 401, "Headers": headers, "Body": []byte("private rejection body")}})
		}
		config := `{"agents":[{"name":"cli","models":["common","custom:a|b"]}],"models":[{"id":"common","maxInputTokens":123456,"maxOutputTokens":4096,"supportsImages":true,"supportsReasoning":true},{"id":"custom:a|b"}]}`
		switch mode {
		case "empty":
			config = `{"agents":[{"name":"cli","models":[]}]}`
		case "missing":
			config = `{"agents":[]}`
		case "malformed":
			config = `{"agents":"broken"}`
		}
		return success(json.RawMessage(config))
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.(workbuddy.ModelResponse), diagnostic.Fields
}

func TestAccountModelSnapshotRecoveryAndRevocation(t *testing.T) {
	for _, site := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(site, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, site, "")
			first, fields := discover(t, s, auth, "network")
			if first.Models == nil || len(first.Models) != 0 || first.AuthUpdate.ID != "" || fields["status"] != "unavailable" {
				t.Fatal("first failure invented models or snapshot")
			}
			ok, fields := discover(t, s, auth, "success")
			prefix := "wb/"
			if site == "workbuddy-ai" {
				prefix = "wb-ai/"
			}
			if len(ok.Models) != 2 || ok.Models[0].ID != prefix+"common" || ok.Models[1].ID != prefix+"custom:a|b" || ok.Models[0].ContextLength != 123456 || fields["status"] != "success" || ok.AuthUpdate.Prefix != "" {
				t.Fatal("account discovery contract mismatch")
			}
			repeated, _ := discover(t, s, ok.AuthUpdate, "success")
			if repeated.AuthUpdate.ID != "" || !reflect.DeepEqual(repeated.Models, ok.Models) {
				t.Fatal("unchanged discovery rewrote credential state")
			}
			s.Close()
			s = workbuddy.New()
			defer s.Close()
			fallback, fields := discover(t, s, ok.AuthUpdate, "network")
			if !reflect.DeepEqual(fallback.Models, ok.Models) || fields["status"] != "snapshot" || fields["snapshot_at"] == nil || fallback.AuthUpdate.ID != "" {
				t.Fatal("restart did not restore exact successful snapshot")
			}
			for _, mode := range []string{"missing", "malformed"} {
				blocked, fields := discover(t, s, ok.AuthUpdate, mode)
				if len(blocked.Models) != 0 || blocked.AuthUpdate.ID == "" || fields["status"] != "unavailable" {
					t.Fatalf("%s retained eligibility", mode)
				}
				after, _ := discover(t, s, blocked.AuthUpdate, "network")
				if len(after.Models) != 0 {
					t.Fatalf("%s resurrected old eligibility", mode)
				}
				recovered, _ := discover(t, s, blocked.AuthUpdate, "success")
				if !reflect.DeepEqual(recovered.Models, ok.Models) {
					t.Fatal("successful discovery did not recover")
				}
			}
			bare, fields := discover(t, s, ok.AuthUpdate, "bare401")
			if fields["reason"] != "upstream_failure" || bare.AuthUpdate.ID != "" || len(bare.Models) != 0 {
				t.Fatal("bare401 made a permanent authentication judgment")
			}
			empty, fields := discover(t, s, ok.AuthUpdate, "empty")
			if empty.Models == nil || len(empty.Models) != 0 || fields["status"] != "empty" || empty.AuthUpdate.ID == "" {
				t.Fatal("valid empty config did not replace snapshot")
			}
			after, fields := discover(t, s, empty.AuthUpdate, "network")
			if len(after.Models) != 0 || fields["status"] != "snapshot" {
				t.Fatal("valid empty snapshot was treated as missing")
			}
		})
	}
}

func TestModelSnapshotRejectsOtherIdentityAndCorruption(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	origin, _ := discover(t, s, parsedIdentity(t, "workbuddy", ""), "success")
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(origin.AuthUpdate.StorageJSON, &saved); err != nil {
		t.Fatal(err)
	}
	for _, target := range []workbuddy.AuthData{parsedIdentity(t, "workbuddy", "enterprise"), parsedIdentity(t, "workbuddy-ai", "")} {
		var body map[string]json.RawMessage
		if err := json.Unmarshal(target.StorageJSON, &body); err != nil {
			t.Fatal(err)
		}
		body["model_snapshot"] = saved["model_snapshot"]
		var err error
		target.StorageJSON, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := discover(t, s, target, "network")
		if len(got.Models) != 0 {
			t.Fatal("snapshot crossed account scope")
		}
	}
	var snapshot map[string]any
	if err := json.Unmarshal(saved["model_snapshot"], &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []any{nil, []any{map[string]any{"id": "bad\nID"}}, []any{map[string]any{"id": "same"}, map[string]any{"id": "same"}}} {
		snapshot["models"] = corrupt
		var err error
		saved["model_snapshot"], err = json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		auth := origin.AuthUpdate
		auth.StorageJSON, err = json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := discover(t, s, auth, "network")
		if len(got.Models) != 0 {
			t.Fatal("malformed snapshot granted permissions")
		}
	}
}

func TestModelDiscoveryCallbackFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "log-failure"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy", "")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := invoke(s, ctx, "model.for_auth", map[string]any{
				"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON,
			}, func(method string, _ []byte) ([]byte, error) {
				if method == "host.http.do" {
					return success(json.RawMessage(`{"agents":[{"name":"cli","models":["common"]}]}`))
				}
				if mode == "cancel" {
					cancel()
					return []byte(`{"ok":true,"result":{}}`), nil
				}
				return nil, errors.New("private diagnostic failure")
			})
			detail, ok := errors.AsType[*workbuddy.Error](err)
			want := "host_log_failed"
			if mode == "cancel" {
				want = "model_discovery_cancelled"
			}
			if !ok || detail.Code != want || strings.Contains(err.Error(), "private") {
				t.Fatalf("callback terminal result = %v", err)
			}
		})
	}
}
