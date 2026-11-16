package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

func checkpointModel(t *testing.T, auth workbuddy.AuthData, site, model string) time.Time {
	t.Helper()
	var stored struct {
		Snapshot struct {
			AuthID     string    `json:"auth_id"`
			Site       string    `json:"site"`
			CapturedAt time.Time `json:"captured_at"`
			Models     []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"model_snapshot"`
	}
	if err := json.Unmarshal(auth.StorageJSON, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Snapshot.AuthID != auth.ID || stored.Snapshot.Site != site || stored.Snapshot.CapturedAt.IsZero() || len(stored.Snapshot.Models) != 1 || stored.Snapshot.Models[0].ID != model {
		t.Fatalf("credential checkpoint lost its account catalog: %+v", stored.Snapshot)
	}
	return stored.Snapshot.CapturedAt
}

func TestLoginCatalogCheckpointRestoresDuringDirectoryOutage(t *testing.T) {
	for _, entry := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(entry, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			catalogRequests := 0
			call := func(method string, raw []byte) ([]byte, error) {
				if method == "host.auth.list" {
					return []byte(`{"ok":true,"result":{"files":[]}}`), nil
				}
				if method == "host.log" {
					return []byte(`{"ok":true,"result":{}}`), nil
				}
				if method != "host.http.do" {
					t.Fatalf("credential lifecycle mutated host state through %s", method)
				}
				var req httpRequest
				if err := json.Unmarshal(raw, &req); err != nil {
					return nil, err
				}
				u, err := url.Parse(req.URL)
				if err != nil {
					return nil, err
				}
				expectedHost := "copilot.tencent.com"
				if entry == "workbuddy-ai" {
					expectedHost = "www.workbuddy.ai"
				}
				if u.Host != expectedHost {
					t.Fatal("catalog checkpoint crossed WorkBuddy sites")
				}
				switch u.Path {
				case "/v2/plugin/auth/state":
					return success(stateReply(entry))
				case "/v2/plugin/auth/token":
					return success(tokenReply())
				case "/v2/plugin/login/account":
					return success(map[string]any{"uid": "checkpoint-user", "type": "personal"})
				case "/v3/config":
					catalogRequests++
					if strings.Join(req.Headers["Authorization"], "") != "Bearer browser-access" || strings.Join(req.Headers["X-User-Id"], "") != "checkpoint-user" {
						t.Fatal("catalog checkpoint lost the confirmed account identity")
					}
					return success(json.RawMessage(`{"agents":[{"name":"cli","models":["checkpoint"]}],"models":[{"id":"checkpoint"}]}`))
				default:
					return nil, errors.New("unexpected checkpoint endpoint")
				}
			}
			flow := begin(t, s, entry, call)
			value, err := invoke(s, context.Background(), "auth.login.poll", map[string]any{"Provider": entry, "State": flow.State, "host_callback_id": "checkpoint-context"}, call)
			if err != nil {
				t.Fatal(err)
			}
			completed := value.(workbuddy.PollResponse)
			if completed.Status != "success" || catalogRequests != 1 {
				t.Fatal("login did not return its account catalog checkpoint")
			}
			captured := checkpointModel(t, completed.Auth, entry, "checkpoint")
			restored := parseRefreshAuth(t, completed.Auth.StorageJSON)
			if checkpointModel(t, restored, entry, "checkpoint") != captured {
				t.Fatal("credential reload changed the catalog sampling time")
			}
			restarted := workbuddy.New()
			defer restarted.Close()
			result, err := invoke(restarted, context.Background(), "model.for_auth", refreshWire(restored), func(method string, _ []byte) ([]byte, error) {
				if method == "host.log" {
					return []byte(`{"ok":true,"result":{}}`), nil
				}
				if method != "host.http.do" {
					t.Fatalf("directory outage invoked %s", method)
				}
				return nil, errors.New("isolated directory outage")
			})
			if err != nil {
				t.Fatal(err)
			}
			models := result.(workbuddy.ModelResponse).Models
			want := "wb/checkpoint"
			if entry == "workbuddy-ai" {
				want = "wb-ai/checkpoint"
			}
			if len(models) != 1 || models[0].ID != want {
				t.Fatalf("checkpoint did not preserve account routing during outage: %+v", models)
			}
		})
	}
}

func TestCatalogOutageKeepsRenewedTokensAndSavedSnapshot(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", time.Hour)
	h := newRotationHost(t, auth)
	first, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := first.(workbuddy.RefreshResponse).Auth
	captured := checkpointModel(t, checkpoint, "workbuddy", "common")
	h.onRefresh = func(httpRequest) ([]byte, error) {
		return success(map[string]any{"accessToken": "access-verified", "refreshToken": "refresh-verified", "expiresIn": 3600})
	}
	second, err := invoke(s, context.Background(), "auth.refresh", refreshWire(checkpoint), func(method string, raw []byte) ([]byte, error) {
		if method == "host.http.do" {
			var req httpRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
			if strings.HasSuffix(req.URL, "/v3/config") {
				return nil, errors.New("isolated catalog outage after token renewal")
			}
		}
		return h.call(method, raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	got := second.(workbuddy.RefreshResponse).Auth
	if got.Metadata["access_token"] != "access-verified" || got.Metadata["refresh_token"] != "refresh-verified" || checkpointModel(t, got, "workbuddy", "common") != captured || h.saves != 0 {
		t.Fatal("catalog outage discarded renewed credentials or the persisted checkpoint")
	}
}
