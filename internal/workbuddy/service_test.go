package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

type hostCall func(string, []byte) ([]byte, error)

type httpRequest struct {
	Method     string              `json:"method"`
	URL        string              `json:"url"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
	CallbackID string              `json:"host_callback_id"`
}

func response(status int, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"StatusCode": status, "Headers": map[string][]string{}, "Body": raw}})
}

func success(data any) ([]byte, error) {
	return response(200, map[string]any{"code": 0, "data": data})
}

func host(t *testing.T, handle func(httpRequest) ([]byte, error)) hostCall {
	t.Helper()
	return func(method string, raw []byte) ([]byte, error) {
		if method == "host.auth.list" {
			return []byte(`{"ok":true,"result":{"files":[]}}`), nil
		}
		if method == "host.log" {
			return []byte(`{"ok":true,"result":{}}`), nil
		}
		if method != "host.http.do" {
			t.Errorf("callback method = %s", method)
			return nil, errors.New("unexpected callback")
		}
		var req httpRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Error("invalid host request JSON")
			return nil, err
		}
		if strings.HasSuffix(req.URL, "/v3/config") {
			return success(json.RawMessage(`{"agents":[{"name":"cli","models":[]}]}`))
		}
		return handle(req)
	}
}

func invoke(s *workbuddy.Service, ctx context.Context, method string, req any, call hostCall) (any, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return s.Handle(ctx, method, raw, call)
}

func begin(t *testing.T, s *workbuddy.Service, id string, call hostCall) workbuddy.StartResponse {
	t.Helper()
	got, err := invoke(s, context.Background(), "auth.login.start", map[string]any{
		"Provider": id, "BaseURL": "http://127.0.0.1:8317/v0/management/oauth-callback", "Metadata": map[string]any{"site": id, "is_webui": "true"}, "host_callback_id": "start-callback",
	}, call)
	if err != nil {
		t.Fatal(err)
	}
	return got.(workbuddy.StartResponse)
}

func poll(t *testing.T, s *workbuddy.Service, ctx context.Context, state string, call hostCall) workbuddy.PollResponse {
	t.Helper()
	got, err := invoke(s, ctx, "auth.login.poll", map[string]any{
		"Provider": "workbuddy", "State": state, "host_callback_id": "poll-callback",
	}, call)
	if err != nil {
		t.Fatal(err)
	}
	return got.(workbuddy.PollResponse)
}

func stateReply(id string) map[string]any {
	hostname := "www.workbuddy.cn"
	if id == "workbuddy-ai" {
		hostname = "www.workbuddy.ai"
	}
	return map[string]any{"state": "upstream-" + id, "authUrl": "https://" + hostname + "/login?platform=" + id + "&state=upstream-" + id}
}

func tokenReply() map[string]any {
	return map[string]any{
		"accessToken": "browser-access", "refreshToken": "browser-refresh", "expiresAt": 1893456000000,
		"refreshExpiresAt": 1924992000000, "domain": "account.workbuddy", "tokenType": "Bearer",
	}
}

func TestLoginRequiresKnownEntryAndBrowserOnlyInputs(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	for _, req := range []map[string]any{
		{"Provider": ""},
		{"Provider": "workbuddy", "Metadata": map[string]any{"site": "other"}},
		{"Provider": "workbuddy", "Metadata": map[string]any{"site": true}},
		{"Provider": "workbuddy", "Metadata": map[string]any{"site": "workbuddy-ai"}},
		{"Provider": "workbuddy-ai", "Metadata": map[string]any{"site": "workbuddy"}},
		{"Provider": "workbuddy", "Metadata": map[string]any{"site": "workbuddy", "endpoint": "https://other.invalid"}},
		{"Provider": "workbuddy", "Metadata": map[string]any{"site": "workbuddy", "api_key": "key"}},
		{"Provider": "workbuddy", "Metadata": map[string]any{"site": "workbuddy", "desktop": true}},
		{"Provider": "other", "Metadata": map[string]any{"site": "workbuddy"}},
	} {
		_, err := invoke(s, context.Background(), "auth.login.start", req, func(string, []byte) ([]byte, error) {
			t.Error("invalid login made an HTTP request")
			return nil, nil
		})
		if err == nil {
			t.Error("invalid login input was accepted")
		}
	}
	got, err := s.Handle(context.Background(), "auth.identifier", []byte("{}"), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Identifier          string   `json:"identifier"`
		Identifiers         []string `json:"identifiers"`
		CredentialProviders []string `json:"credential_providers"`
		Entries             []struct {
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Identifier != "workbuddy" || len(wire.Identifiers) != 2 || wire.Identifiers[0] != "workbuddy" || wire.Identifiers[1] != "workbuddy-ai" || len(wire.CredentialProviders) != 1 || wire.CredentialProviders[0] != "workbuddy" {
		t.Fatal("schema-10 login entries or credential provider routing changed")
	}
	if len(wire.Entries) != 2 || wire.Entries[0].Identifier != "workbuddy" || wire.Entries[0].Name != "WorkBuddy国内" || wire.Entries[1].Identifier != "workbuddy-ai" || wire.Entries[1].Name != "WorkBuddy国际" {
		t.Fatal("schema-10 login entry display names changed")
	}
}

func TestLoginURLTrustAndOpaqueSessionState(t *testing.T) {
	for _, tc := range []struct {
		site  string
		page  string
		valid bool
	}{
		{"workbuddy", "https://www.workbuddy.cn/login?platform=workbuddy&state=upstream", true},
		{"workbuddy", "https://www.codebuddy.cn/login", true},
		{"workbuddy-ai", "https://www.workbuddy.ai/login", true},
		{"workbuddy", "http://www.workbuddy.cn/login", false},
		{"workbuddy", "https://www.workbuddy.cn.evil.invalid/login", false},
		{"workbuddy", "https://www.workbuddy.cn:8443/login", false},
		{"workbuddy", "https://user@www.workbuddy.cn/login", false},
		{"workbuddy", "https://www.workbuddy.ai/login", false},
		{"workbuddy-ai", "https://www.workbuddy.cn/login", false},
	} {
		t.Run(tc.site+"/"+tc.page, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			call := host(t, func(req httpRequest) ([]byte, error) {
				u, err := url.Parse(req.URL)
				if err != nil {
					t.Fatal(err)
				}
				expectedHost := "copilot.tencent.com"
				if tc.site == "workbuddy-ai" {
					expectedHost = "www.workbuddy.ai"
				}
				if u.Scheme != "https" || u.Host != expectedHost || u.Path != "/v2/plugin/auth/state" || u.Query().Get("platform") != tc.site || req.Method != "POST" || string(req.Body) != "{}" || req.CallbackID != "start-callback" {
					t.Error("state request did not follow the fixed host contract")
				}
				for _, name := range []string{"X-No-Authorization", "X-No-User-Id", "X-No-Enterprise-Id", "X-No-Department-Info"} {
					if len(req.Headers[name]) != 1 || req.Headers[name][0] != "true" {
						t.Errorf("missing state header %s", name)
					}
				}
				if len(req.Headers["Authorization"]) != 0 {
					t.Error("state request sent credentials")
				}
				return success(map[string]any{"state": "upstream", "authUrl": tc.page})
			})
			before := time.Now()
			got, err := invoke(s, context.Background(), "auth.login.start", map[string]any{"Provider": tc.site, "BaseURL": "http://127.0.0.1:8317/v0/management/oauth-callback", "Metadata": map[string]any{"site": tc.site, "is_webui": "true"}, "host_callback_id": "start-callback"}, call)
			if !tc.valid {
				if err == nil {
					t.Fatal("untrusted browser URL was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			flow := got.(workbuddy.StartResponse)
			page, _ := url.Parse(flow.URL)
			if page.Query().Get("version") != "5.9.3" || len(page.Query().Get("loginSessionId")) != 64 || len(flow.State) != 64 || flow.State == page.Query().Get("loginSessionId") || flow.State == "upstream" {
				t.Fatal("browser version or independent random states are missing")
			}
			if flow.ExpiresAt.Before(before.Add(5*time.Minute)) || flow.ExpiresAt.After(time.Now().Add(5*time.Minute)) {
				t.Fatal("login session is not bounded to five minutes")
			}
		})
	}
}

func TestConcurrentSitesRestoreCredentialsAndConsumeSuccess(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	call := host(t, func(req httpRequest) ([]byte, error) {
		u, _ := url.Parse(req.URL)
		id := "workbuddy"
		if u.Host == "www.workbuddy.ai" {
			id = "workbuddy-ai"
		} else if u.Host != "copilot.tencent.com" {
			t.Error("request used an unconfigured endpoint")
		}
		switch u.Path {
		case "/v2/plugin/auth/state":
			return success(stateReply(id))
		case "/v2/plugin/auth/token":
			if u.Query().Get("state") != "upstream-"+id || req.CallbackID != "poll-callback" || len(req.Headers["Authorization"]) != 0 {
				t.Error("poll state, callback, or token headers crossed login sessions")
			}
			return success(tokenReply())
		case "/v2/plugin/login/account":
			if u.Query().Get("state") != "upstream-"+id || req.Headers["Authorization"][0] != "Bearer browser-access" || req.Headers["X-Domain"][0] != "account.workbuddy" || req.CallbackID != "poll-callback" {
				t.Error("account request lost the token or callback binding")
			}
			return success(map[string]any{"uid": "same-user", "nickname": "Person", "type": "personal"})
		default:
			t.Error("personal login requested enterprise accounts")
			return nil, errors.New("unexpected request")
		}
	})
	cn := begin(t, s, "workbuddy", call)
	ai := begin(t, s, "workbuddy-ai", call)
	if cn.State == ai.State {
		t.Fatal("sites share a local login state")
	}
	type result struct {
		value any
		err   error
	}
	results := make(chan result, 2)
	for _, flow := range []workbuddy.StartResponse{cn, ai} {
		go func(flow workbuddy.StartResponse) {
			value, err := invoke(s, context.Background(), "auth.login.poll", map[string]any{"Provider": flow.Provider, "State": flow.State, "host_callback_id": "poll-callback"}, call)
			results <- result{value: value, err: err}
		}(flow)
	}
	ids := make(map[string]bool)
	restarted := workbuddy.New()
	defer restarted.Close()
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		got := result.value.(workbuddy.PollResponse)
		if got.Status != "success" || got.Auth.Provider != "workbuddy" || got.Auth.Prefix != "" || !strings.Contains(got.Auth.FileName, "Person") || !strings.HasSuffix(got.Auth.FileName, ".json") || strings.Contains(got.Auth.FileName, "same-user") {
			t.Fatal("login did not produce a scoped auth record with a readable credential filename")
		}
		if ids[got.Auth.ID] {
			t.Fatal("same uid on two sites shared a host identity")
		}
		ids[got.Auth.ID] = true
		if got.Auth.Metadata["access_token"] != "browser-access" || got.Auth.Metadata["refresh_token"] != "browser-refresh" || got.Auth.Metadata["expires_at"] != "2030-01-01T00:00:00Z" || got.Auth.Metadata["refresh_expires_at"] != "2031-01-01T00:00:00Z" || got.Auth.Metadata["token_type"] != "Bearer" || got.Auth.Attributes["scope"] != "personal" {
			t.Fatal("host metadata lost credential fields or absolute token expiry")
		}
		// CPA merges metadata into provider storage and sets type=provider
		// when atomically saving. Restore that actual persisted shape.
		saved := got.Auth.Metadata
		saved["type"] = "workbuddy"
		saved["disabled"] = true
		saved["priority"] = 7
		raw, _ := json.Marshal(saved)
		value, err := invoke(restarted, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": raw}, nil)
		if err != nil {
			t.Fatal(err)
		}
		restored := value.(workbuddy.ParseResponse)
		if !restored.Handled || restored.Auth.ID != got.Auth.ID || restored.Auth.FileName != got.Auth.FileName || !restored.Auth.Disabled || restored.Auth.Metadata["priority"] != float64(7) || restored.Auth.Metadata["site"] != got.Auth.Metadata["site"] {
			t.Fatal("host-persisted credentials did not restore their identity and metadata")
		}
	}
	for _, flow := range []workbuddy.StartResponse{cn, ai} {
		if got := poll(t, s, context.Background(), flow.State, nil); got.Status != "error" || got.Auth.Provider != "" {
			t.Fatal("consumed state returned credentials again")
		}
		if got := poll(t, restarted, context.Background(), flow.State, nil); got.Status != "error" {
			t.Fatal("restart fabricated a completed login session")
		}
	}
}

func TestPendingPollsRetainTokenAndOnlyKnownFailuresWait(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	tokens, accounts := 0, 0
	call := host(t, func(req httpRequest) ([]byte, error) {
		u, _ := url.Parse(req.URL)
		switch u.Path {
		case "/v2/plugin/auth/state":
			return success(stateReply("workbuddy"))
		case "/v2/plugin/auth/token":
			tokens++
			switch tokens {
			case 1:
				return response(200, map[string]any{"code": "11217", "msg": "secret upstream detail"})
			case 2:
				return success(map[string]any{})
			case 3:
				return response(429, map[string]any{"msg": "rate limit detail"})
			default:
				return success(map[string]any{"accessToken": "retained-token", "expiresIn": 3600, "refreshExpiresIn": 7200})
			}
		case "/v2/plugin/login/account":
			accounts++
			if req.Headers["Authorization"][0] != "Bearer retained-token" || req.Headers["X-Domain"][0] != "copilot.tencent.com" {
				t.Error("pending account lost token or default domain")
			}
			switch accounts {
			case 1:
				return response(200, map[string]any{"code": 12151})
			case 2:
				return response(408, map[string]any{})
			case 3:
				return success(map[string]any{})
			default:
				return success(map[string]any{"uid": "u"})
			}
		}
		return nil, errors.New("unexpected request")
	})
	flow := begin(t, s, "workbuddy", call)
	before := time.Now()
	for range 6 {
		if got := poll(t, s, context.Background(), flow.State, call); got.Status != "pending" || strings.Contains(got.Message, "secret") {
			t.Fatal("known pending or transient response did not wait safely")
		}
	}
	got := poll(t, s, context.Background(), flow.State, call)
	if got.Status != "success" || tokens != 4 || accounts != 4 {
		t.Fatal("account polling re-fetched an already obtained token")
	}
	expires, err := time.Parse(time.RFC3339Nano, got.Auth.Metadata["expires_at"].(string))
	if err != nil || expires.Before(before.Add(time.Hour-time.Second)) || expires.After(time.Now().Add(time.Hour)) {
		t.Fatal("relative access expiry was not resolved to an absolute host expiry")
	}
}

func TestTerminalPollFailuresDiscardSessionWithoutSecretDetails(t *testing.T) {
	for _, tc := range []struct {
		name          string
		path          string
		status        int
		code          any
		transport     bool
		invalidToken  bool
		malformedData bool
	}{
		{name: "refused-token", path: "/v2/plugin/auth/token", status: 401, code: 11001},
		{name: "server-error", path: "/v2/plugin/auth/token", status: 500, code: 500},
		{name: "wrong-stage-pending-code", path: "/v2/plugin/auth/token", status: 200, code: 12151},
		{name: "refused-account", path: "/v2/plugin/login/account", status: 403, code: 11004},
		{name: "transport", path: "/v2/plugin/auth/token", transport: true},
		{name: "missing-expiry", path: "/v2/plugin/auth/token", invalidToken: true},
		{name: "malformed-token", path: "/v2/plugin/auth/token", malformedData: true},
		{name: "malformed-account", path: "/v2/plugin/login/account", malformedData: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			call := host(t, func(req httpRequest) ([]byte, error) {
				u, _ := url.Parse(req.URL)
				if u.Path == "/v2/plugin/auth/state" {
					return success(stateReply("workbuddy"))
				}
				if u.Path == tc.path {
					if tc.transport {
						return nil, errors.New("upstream-state secret-token")
					}
					if tc.malformedData {
						return success(map[string]any{"unrecognized": "data"})
					}
					if tc.invalidToken {
						return success(map[string]any{"accessToken": "secret-token"})
					}
					return response(tc.status, map[string]any{"code": tc.code, "msg": "upstream-state secret-token"})
				}
				return success(tokenReply())
			})
			flow := begin(t, s, "workbuddy", call)
			got := poll(t, s, context.Background(), flow.State, call)
			if got.Status != "error" || strings.Contains(got.Message, "secret") || strings.Contains(got.Message, "upstream-state") || got.Auth.Provider != "" {
				t.Fatal("terminal failure was retried or exposed upstream details")
			}
			if got := poll(t, s, context.Background(), flow.State, nil); got.Status != "error" {
				t.Fatal("terminal session was retained")
			}
		})
	}
}

func TestEnterpriseSelectionPreservesBrowserScope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		who      map[string]any
		accounts []map[string]any
		want     string
		wantName string
		list     bool
		fail     bool
	}{
		{name: "explicit", who: map[string]any{"uid": "u", "nickname": "Person", "type": "ultimate", "enterpriseId": "ent-1", "enterpriseName": "School"}, want: "ent-1", wantName: "School"},
		{name: "enriched", who: map[string]any{"uid": "u", "type": "ultimate", "enterpriseId": "ent-1"}, accounts: []map[string]any{{"uid": "u", "enterpriseId": "ent-1", "enterpriseName": "School"}}, want: "ent-1", wantName: "School", list: true},
		{name: "unique-type", who: map[string]any{"uid": "u", "type": "ultimate"}, accounts: []map[string]any{{"uid": "u", "type": "ultimate", "enterpriseId": "ent-2"}, {"uid": "other", "type": "ultimate", "enterpriseId": "wrong"}}, want: "ent-2", list: true},
		{name: "unique-last-login", who: map[string]any{"uid": "u", "type": "ultimate"}, accounts: []map[string]any{{"uid": "u", "type": "ultimate", "enterpriseId": "ent-1"}, {"uid": "u", "type": "ultimate", "enterpriseId": "ent-2", "lastLogin": true}}, want: "ent-2", list: true},
		{name: "personal", who: map[string]any{"uid": "u", "type": "personal"}, accounts: []map[string]any{{"uid": "u", "type": "ultimate", "enterpriseId": "wrong", "lastLogin": true}}},
		{name: "ambiguous", who: map[string]any{"uid": "u", "type": "ultimate"}, accounts: []map[string]any{{"uid": "u", "type": "ultimate", "enterpriseId": "ent-1"}, {"uid": "u", "type": "ultimate", "enterpriseId": "ent-2"}}, fail: true, list: true},
		{name: "unresolved", who: map[string]any{"uid": "u", "type": "ultimate"}, fail: true, list: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			listed := false
			call := host(t, func(req httpRequest) ([]byte, error) {
				u, _ := url.Parse(req.URL)
				switch u.Path {
				case "/v2/plugin/auth/state":
					return success(stateReply("workbuddy"))
				case "/v2/plugin/auth/token":
					return success(tokenReply())
				case "/v2/plugin/login/account":
					return success(tc.who)
				case "/v2/plugin/accounts":
					listed = true
					if len(req.Headers["X-Enterprise-Id"]) != 0 || len(req.Headers["X-Tenant-Id"]) != 0 {
						t.Error("enterprise discovery selected a scope before resolving the account")
					}
					return success(map[string]any{"accounts": tc.accounts})
				}
				return nil, errors.New("unexpected request")
			})
			flow := begin(t, s, "workbuddy", call)
			got := poll(t, s, context.Background(), flow.State, call)
			if listed != tc.list {
				t.Fatal("account list lookup did not respect the login's scope evidence")
			}
			if tc.fail {
				if got.Status != "error" {
					t.Fatal("ambiguous enterprise became a personal login")
				}
				return
			}
			if got.Status != "success" {
				t.Fatal("identified account did not sign in")
			}
			if tc.want == "" {
				if got.Auth.Metadata["enterprise_id"] != "" || got.Auth.Attributes["scope"] != "personal" {
					t.Fatal("personal login switched to an enterprise")
				}
			} else if got.Auth.Metadata["enterprise_id"] != tc.want || got.Auth.Attributes["scope"] != "enterprise:"+tc.want || (tc.wantName != "" && !strings.Contains(got.Auth.Label, tc.wantName)) {
				t.Fatal("selected enterprise was lost")
			}
		})
	}
}

func TestParserDoesNotClaimForeignOrUnversionedCredentials(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	for _, raw := range []string{
		`{"type":"other","access_token":"token"}`,
		`{"type":"workbuddy","access_token":"token"}`,
		`{"provider":"workbuddy","type":"workbuddy","credential_type":"browser","version":2}`,
		`{"provider":"other","type":"workbuddy","credential_type":"browser","version":1}`,
		`{"provider":"workbuddy","type":"workbuddy","credential_type":"api_key","version":1}`,
		`{"provider":"workbuddy","type":"workbuddy","credential_type":"desktop","version":1}`,
	} {
		got, err := invoke(s, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": []byte(raw)}, nil)
		if err != nil || got.(workbuddy.ParseResponse).Handled {
			t.Fatal("parser claimed foreign, desktop, or unknown-version credentials")
		}
	}
	base := map[string]any{"provider": "workbuddy", "type": "workbuddy", "credential_type": "browser", "version": 1, "site": "workbuddy", "uid": "u", "access_token": "a", "expires_at": "2030-01-01T00:00:00Z"}
	for _, field := range []string{"site", "uid", "access_token", "expires_at"} {
		broken := make(map[string]any)
		for key, value := range base {
			if key != field {
				broken[key] = value
			}
		}
		raw, _ := json.Marshal(broken)
		if _, err := invoke(s, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": raw}, nil); err == nil {
			t.Errorf("credentials missing %s were accepted", field)
		}
	}
	base["endpoint"] = "https://other.invalid"
	raw, _ := json.Marshal(base)
	if _, err := invoke(s, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": raw}, nil); err == nil {
		t.Fatal("parser accepted an arbitrary endpoint")
	}
}

func TestSameSessionPollsSerializeAndDeliverCredentialsOnce(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	var tokenCalls atomic.Int32
	call := host(t, func(req httpRequest) ([]byte, error) {
		u, _ := url.Parse(req.URL)
		switch u.Path {
		case "/v2/plugin/auth/state":
			return success(stateReply("workbuddy"))
		case "/v2/plugin/auth/token":
			if tokenCalls.Add(1) == 1 {
				close(entered)
				<-release
			}
			return success(tokenReply())
		default:
			return success(map[string]any{"uid": "u", "type": "personal"})
		}
	})
	flow := begin(t, s, "workbuddy", call)
	results := make(chan workbuddy.PollResponse, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			value, err := invoke(s, context.Background(), "auth.login.poll", map[string]any{"Provider": "workbuddy", "State": flow.State}, call)
			if err != nil {
				t.Error(err)
				results <- workbuddy.PollResponse{}
				return
			}
			results <- value.(workbuddy.PollResponse)
		}()
	}
	ready.Wait()
	<-entered
	close(release)
	statuses := make(map[string]int)
	for range 2 {
		statuses[(<-results).Status]++
	}
	if statuses["success"] != 1 || statuses["error"] != 1 || tokenCalls.Load() != 1 {
		t.Fatal("overlapping polls duplicated token requests or delivered credentials twice")
	}
}

func TestCancellationExpiryAndCloseDiscardSessions(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			entered := make(chan struct{})
			release := make(chan struct{})
			call := host(t, func(req httpRequest) ([]byte, error) {
				u, _ := url.Parse(req.URL)
				if u.Path == "/v2/plugin/auth/state" {
					return success(stateReply("workbuddy"))
				}
				close(entered)
				<-release
				return success(tokenReply())
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer deadlineCancel()
			}
			flow := begin(t, s, "workbuddy", call)
			results := make(chan any, 1)
			go func() {
				value, err := invoke(s, ctx, "auth.login.poll", map[string]any{"Provider": "workbuddy", "State": flow.State}, call)
				if err != nil {
					results <- err
					return
				}
				results <- value
			}()
			<-entered
			var closed chan struct{}
			switch mode {
			case "cancel":
				cancel()
			case "deadline":
				<-ctx.Done()
			case "close":
				closed = make(chan struct{})
				go func() {
					s.Close()
					close(closed)
				}()
			}
			if closed != nil {
				awaitServiceClosed(t, s)
			}
			close(release)
			if closed != nil {
				<-closed
			}
			result := <-results
			got, ok := result.(workbuddy.PollResponse)
			if !ok || got.Status != "error" || got.Auth.Provider != "" {
				t.Fatal("cancelled session returned credentials")
			}
			_, err := invoke(s, context.Background(), "auth.login.poll", map[string]any{"Provider": "workbuddy", "State": flow.State}, nil)
			if mode == "close" {
				if err == nil {
					t.Fatal("closed service accepted new work")
				}
			} else if err != nil {
				t.Fatal(err)
			} else if got := poll(t, s, context.Background(), flow.State, nil); got.Status != "error" {
				t.Fatal("cancelled session remained usable")
			}
		})
	}
}

func TestHostDeadlineBoundsSessionAndExpiryIsObservable(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	call := host(t, func(req httpRequest) ([]byte, error) {
		return success(stateReply("workbuddy"))
	})
	value, err := invoke(s, ctx, "auth.login.start", map[string]any{"Provider": "workbuddy", "Metadata": map[string]any{"site": "workbuddy"}}, call)
	if err != nil {
		t.Fatal(err)
	}
	flow := value.(workbuddy.StartResponse)
	if !flow.ExpiresAt.Equal(deadline) {
		t.Fatal("host deadline did not shorten the five minute login window")
	}
	<-ctx.Done()
	got := poll(t, s, context.Background(), flow.State, nil)
	if got.Status != "error" || got.Auth.Provider != "" {
		t.Fatal("expired login state remained usable")
	}
}

func TestIdentitySurvivesNicknameChangesAndSeparatesScopes(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	ids := make([]string, 0, 4)
	for _, tc := range []struct {
		site       string
		name       string
		enterprise string
	}{
		{site: "workbuddy", name: "Old name"},
		{site: "workbuddy", name: "New name"},
		{site: "workbuddy", name: "New name", enterprise: "team-1"},
		{site: "workbuddy", name: "New name", enterprise: "team-2"},
		{site: "workbuddy-ai", name: "New name"},
	} {
		raw, err := json.Marshal(map[string]any{
			"provider": "workbuddy", "type": "workbuddy", "credential_type": "browser", "version": 1,
			"site": tc.site, "uid": "same-person", "enterprise_id": tc.enterprise,
			"display_name": tc.name, "access_token": "browser-access", "expires_at": "2030-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatal(err)
		}
		value, err := invoke(s, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": raw}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := value.(workbuddy.ParseResponse)
		if !got.Handled || !strings.Contains(got.Auth.Label, tc.name) || strings.Contains(got.Auth.FileName, "same-person") {
			t.Fatal("restored auth lost its display label or opaque file name")
		}
		ids = append(ids, got.Auth.ID)
	}
	if ids[0] != ids[1] {
		t.Fatal("nickname change created a new credential identity")
	}
	seen := map[string]bool{ids[0]: true}
	for _, id := range ids[2:] {
		if seen[id] {
			t.Fatal("site or enterprise scope collided with another credential")
		}
		seen[id] = true
	}
}

func TestIdentifierRejectsInvalidJSON(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	for _, raw := range []string{"", "null", "[]", "{", `{} {}`} {
		if _, err := s.Handle(context.Background(), "auth.identifier", []byte(raw), nil); err == nil {
			t.Fatal("identifier accepted a malformed RPC object")
		}
	}
}

func TestSessionAdmissionLimitRecoversAfterCancellation(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	calls := 0
	call := host(t, func(req httpRequest) ([]byte, error) {
		calls++
		return success(stateReply("workbuddy"))
	})
	first := begin(t, s, "workbuddy", call)
	for range 127 {
		begin(t, s, "workbuddy", call)
	}
	_, err := invoke(s, context.Background(), "auth.login.start", map[string]any{"Provider": "workbuddy", "Metadata": map[string]any{"site": "workbuddy"}}, call)
	busy, ok := err.(*workbuddy.Error)
	if !ok || busy.Code != "login_busy" || busy.HTTPStatus != 429 || calls != 128 {
		t.Fatal("session limit did not return an explicit busy error before creating more upstream states")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := poll(t, s, ctx, first.State, nil); got.Status != "error" {
		t.Fatal("cancelled login did not release its session")
	}
	if flow := begin(t, s, "workbuddy", call); flow.State == "" || calls != 129 {
		t.Fatal("released session capacity did not admit a new login")
	}
}

func TestLoginEntriesSelectSiteAndRejectCrossSitePolling(t *testing.T) {
	for _, entry := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(entry, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			calls := 0
			endpoint := "copilot.tencent.com"
			other := "workbuddy-ai"
			if entry == "workbuddy-ai" {
				endpoint = "www.workbuddy.ai"
				other = "workbuddy"
			}
			call := host(t, func(req httpRequest) ([]byte, error) {
				calls++
				u, err := url.Parse(req.URL)
				if err != nil || u.Host != endpoint {
					t.Fatal("login entry crossed site endpoints")
				}
				switch u.Path {
				case "/v2/plugin/auth/state":
					if u.Query().Get("platform") != entry {
						t.Fatal("login entry did not select the upstream platform")
					}
					return success(stateReply(entry))
				case "/v2/plugin/auth/token":
					return success(tokenReply())
				case "/v2/plugin/login/account":
					return success(map[string]any{"uid": "person", "nickname": "Person", "type": "personal"})
				default:
					return nil, errors.New("unexpected login endpoint")
				}
			})
			value, err := invoke(s, context.Background(), "auth.login.start", map[string]any{"Provider": entry, "Metadata": map[string]any{"is_webui": "true"}}, call)
			if err != nil {
				t.Fatal(err)
			}
			flow := value.(workbuddy.StartResponse)
			if flow.Provider != entry || flow.Metadata["site"] != entry {
				t.Fatal("login entry was not bound to the returned session")
			}
			for _, req := range []map[string]any{
				{"Provider": other, "State": flow.State},
				{"Provider": entry, "State": flow.State, "Metadata": map[string]any{"site": other}},
				{"Provider": other, "State": flow.State, "Metadata": map[string]any{"site": entry}},
			} {
				_, err := invoke(s, context.Background(), "auth.login.poll", req, call)
				if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.Code != "invalid_site" || detail.HTTPStatus != 400 {
					t.Fatalf("cross-site poll = %v", err)
				}
			}
			if calls != 1 {
				t.Fatal("cross-site poll reached upstream")
			}
			value, err = invoke(s, context.Background(), "auth.login.poll", map[string]any{"Provider": entry, "State": flow.State}, call)
			if err != nil {
				t.Fatal(err)
			}
			got := value.(workbuddy.PollResponse)
			if got.Status != "success" || got.Auth.Provider != "workbuddy" || got.Auth.Metadata["site"] != entry || calls != 3 {
				t.Fatal("valid poll lost its site or persistent provider after rejected cross-site attempts")
			}
		})
	}
}
