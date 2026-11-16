package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"cpa-workbuddy/internal/workbuddy"
)

func namedCredential(t *testing.T, service *workbuddy.Service, overrides map[string]any, source string) workbuddy.AuthData {
	t.Helper()
	fields := map[string]any{
		"provider": "workbuddy", "type": "workbuddy", "credential_type": "browser", "version": 1,
		"site": "workbuddy-ai", "uid": "named-user", "enterprise_id": "", "display_name": "张三",
		"access_token": "named-access", "expires_at": "2030-01-01T00:00:00Z",
	}
	maps.Copy(fields, overrides)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	result, err := invoke(service, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": raw, "FileName": source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result.(workbuddy.ParseResponse).Auth
}

func TestReadableCredentialNamesSeparateAccountsAndScopes(t *testing.T) {
	service := workbuddy.New()
	defer service.Close()
	ids, names := map[string]bool{}, map[string]bool{}
	for _, fields := range []map[string]any{
		{},
		{"uid": "another-user"},
		{"site": "workbuddy"},
		{"enterprise_id": "team-one"},
		{"enterprise_id": "team-two"},
	} {
		auth := namedCredential(t, service, fields, "")
		if !strings.Contains(auth.FileName, "张三") || !strings.HasSuffix(auth.FileName, ".json") || strings.Contains(auth.ID, "张三") {
			t.Fatal("credential filename and stable authentication identity do not express their separate roles")
		}
		if ids[auth.ID] || names[auth.FileName] {
			t.Fatal("same-label accounts or scopes share an identity or credential file")
		}
		ids[auth.ID], names[auth.FileName] = true, true
		var stored struct {
			FileName string `json:"file_name"`
		}
		if err := json.Unmarshal(auth.StorageJSON, &stored); err != nil {
			t.Fatal(err)
		}
		if stored.FileName != auth.FileName {
			t.Fatal("credential did not persist its canonical filename")
		}
		if _, err := auth.RequestHeaders(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCredentialSourceFileNameSurvivesRestoreAndRelogin(t *testing.T) {
	service := workbuddy.New()
	defer service.Close()
	const source = "WorkBuddy · 张三-account.JSON"
	auth := namedCredential(t, service, map[string]any{"site": "workbuddy", "uid": "returning-user"}, source)
	if auth.FileName != source {
		t.Fatal("parser changed the host-owned source filename")
	}
	var saved map[string]any
	if err := json.Unmarshal(auth.StorageJSON, &saved); err != nil {
		t.Fatal(err)
	}
	service.Close()
	service = workbuddy.New()
	defer service.Close()
	restored := namedCredential(t, service, saved, "")
	if restored.ID != auth.ID || restored.FileName != source {
		t.Fatal("restart changed the account identity or canonical file")
	}
	httpCall := host(t, func(req httpRequest) ([]byte, error) {
		switch {
		case strings.Contains(req.URL, "/v2/plugin/auth/state"):
			return success(stateReply("workbuddy"))
		case strings.Contains(req.URL, "/v2/plugin/auth/token"):
			return success(tokenReply())
		case strings.Contains(req.URL, "/v2/plugin/login/account"):
			return success(map[string]any{"uid": "returning-user", "nickname": "李四", "type": "personal"})
		default:
			return nil, errors.New("unexpected login request")
		}
	})
	service.Close()
	service = workbuddy.New()
	defer service.Close()
	call := func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.list" {
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"files": []map[string]string{{"id": auth.ID, "name": source, "provider": "workbuddy"}}}})
		}
		return httpCall(method, raw)
	}
	flow := begin(t, service, "workbuddy", call)
	result := poll(t, service, context.Background(), flow.State, call)
	if result.Status != "success" || result.Auth.ID != auth.ID || result.Auth.FileName != source || !strings.Contains(result.Auth.Label, "李四") {
		t.Fatal("relogin lost its persisted file or latest account label")
	}
}

func TestCredentialNamesUsePortableUTF8Components(t *testing.T) {
	service := workbuddy.New()
	defer service.Close()
	auth := namedCredential(t, service, map[string]any{"display_name": "<>:\"/\\|?*\n" + strings.Repeat("名字🙂", 100)}, "")
	if !utf8.ValidString(auth.FileName) || len(auth.FileName) > 240 {
		t.Fatal("credential filename exceeds the portable component budget")
	}
	for _, char := range auth.FileName {
		if unicode.IsControl(char) || strings.ContainsRune(`<>:"/\|?*`, char) {
			t.Fatal("credential filename contains an invalid filesystem character")
		}
	}
	for _, source := range []string{"../outside.json", filepath.Join(string(filepath.Separator), "outside.json"), "bad\nname.json"} {
		raw, err := json.Marshal(map[string]any{
			"provider": "workbuddy", "type": "workbuddy", "credential_type": "browser", "version": 1,
			"site": "workbuddy-ai", "uid": "named-user", "access_token": "named-access", "expires_at": "2030-01-01T00:00:00Z", "file_name": source,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = invoke(service, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": raw}, nil)
		if err == nil {
			t.Fatalf("accepted unsafe persisted filename %q", source)
		}
	}
}

func TestCredentialSourcePathsPreserveCanonicalIdentity(t *testing.T) {
	service := workbuddy.New()
	defer service.Close()
	original := namedCredential(t, service, nil, "")
	for _, source := range []string{
		filepath.Join("team", "account.json"),
		strings.Repeat("team"+string(filepath.Separator), 60) + "account.JSON",
	} {
		parsed := namedCredential(t, service, nil, source)
		if parsed.ID != original.ID || parsed.FileName != source {
			t.Fatal("host relative filename changed the credential identity or source")
		}
	}
	if runtime.GOOS != "windows" {
		parsed := namedCredential(t, service, nil, "project:account.json")
		if parsed.ID != original.ID || parsed.FileName != "project:account.json" {
			t.Fatal("valid host filename was rejected")
		}
	}
	for _, source := range []string{"../outside.json", "opaque-host-identifier", "bad\nname.json"} {
		parsed := namedCredential(t, service, nil, source)
		if parsed.ID != original.ID || parsed.FileName != original.FileName {
			t.Fatal("optional host identifier changed a recognized credential")
		}
	}
	const canonical = "WorkBuddy · 张三-account.JSON"
	source := strings.ToLower(canonical)
	parsed := namedCredential(t, service, map[string]any{"file_name": canonical}, source)
	want := source
	if runtime.GOOS == "windows" {
		want = canonical
	}
	if parsed.FileName != want {
		t.Fatal("canonical filename did not follow host filesystem case semantics")
	}
}

func TestLoginRejectsUnavailableOrConflictingHostNames(t *testing.T) {
	for _, mode := range []string{"unavailable", "conflict", "foreign-provider", "missing-list"} {
		t.Run(mode, func(t *testing.T) {
			service := workbuddy.New()
			defer service.Close()
			candidate := namedCredential(t, service, map[string]any{"site": "workbuddy", "display_name": "Person"}, "")
			httpCall := host(t, func(req httpRequest) ([]byte, error) {
				switch {
				case strings.Contains(req.URL, "/v2/plugin/auth/state"):
					return success(stateReply("workbuddy"))
				case strings.Contains(req.URL, "/v2/plugin/auth/token"):
					return success(tokenReply())
				case strings.Contains(req.URL, "/v2/plugin/login/account"):
					return success(map[string]any{"uid": "named-user", "nickname": "Person", "type": "personal"})
				default:
					return nil, errors.New("unexpected login request")
				}
			})
			call := func(method string, raw []byte) ([]byte, error) {
				if method != "host.auth.list" {
					return httpCall(method, raw)
				}
				switch mode {
				case "unavailable":
					return nil, errors.New("private host details")
				case "missing-list":
					return []byte(`{"ok":true,"result":{}}`), nil
				case "conflict":
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"files": []map[string]string{{"id": "other-credential", "name": candidate.FileName, "provider": "other"}}}})
				default:
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"files": []map[string]string{{"id": candidate.ID, "name": candidate.FileName, "provider": "other"}}}})
				}
			}
			flow := begin(t, service, "workbuddy", call)
			value, err := invoke(service, context.Background(), "auth.login.poll", map[string]any{"Provider": "workbuddy", "State": flow.State, "host_callback_id": "poll-callback"}, call)
			var public *workbuddy.Error
			want := "host_auth_unavailable"
			switch mode {
			case "conflict":
				want = "credential_name_conflict"
			case "foreign-provider":
				want = "invalid_host_auth"
			}
			if !errors.As(err, &public) || public.Code != want || value != nil {
				t.Fatalf("host metadata failure returned credentials: %v", err)
			}
		})
	}
}
