package workbuddy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

func refreshIdentity(t *testing.T, site, enterprise string, remaining time.Duration) workbuddy.AuthData {
	t.Helper()
	auth := parsedIdentity(t, site, enterprise)
	fields := auth.Metadata
	fields["access_token"] = "access-start"
	fields["refresh_token"] = "refresh-start"
	fields["expires_at"] = time.Now().Add(remaining).UTC().Format(time.RFC3339Nano)
	fields["refresh_expires_at"] = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	fields["disabled"] = true
	fields["priority"] = 7
	fields["proxy_url"] = "http://127.0.0.1:1234"
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return parseRefreshAuth(t, raw)
}

func parseRefreshAuth(t *testing.T, raw []byte) workbuddy.AuthData {
	t.Helper()
	s := workbuddy.New()
	defer s.Close()
	got, err := invoke(s, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": raw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(workbuddy.ParseResponse).Auth
}

type rotationHost struct {
	mu           sync.Mutex
	files        map[string]json.RawMessage
	refreshes    []httpRequest
	opens        []httpRequest
	streamStatus map[string]int
	configTokens []string
	saves        int
	failSave     bool
	rejectOld    bool
	rejectAll    bool
	logs         [][]byte
	onRefresh    func(httpRequest) ([]byte, error)
	onOpen       func()
}

func newRotationHost(t *testing.T, auths ...workbuddy.AuthData) *rotationHost {
	t.Helper()
	h := &rotationHost{files: make(map[string]json.RawMessage), streamStatus: make(map[string]int)}
	for _, auth := range auths {
		raw, err := json.Marshal(auth.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		h.files[auth.ID] = raw
	}
	return h
}

func (h *rotationHost) raw(id string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return bytes.Clone(h.files[id])
}

func (h *rotationHost) replace(id, access, refresh string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var fields map[string]any
	if err := json.Unmarshal(h.files[id], &fields); err != nil {
		return err
	}
	fields["access_token"] = access
	fields["refresh_token"] = refresh
	fields["expires_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(fields)
	if err == nil {
		h.files[id] = raw
	}
	return err
}

func (h *rotationHost) call(method string, raw []byte) ([]byte, error) {
	switch method {
	case "host.auth.list":
		h.mu.Lock()
		defer h.mu.Unlock()
		files := []map[string]string{}
		for id := range h.files {
			files = append(files, map[string]string{"id": id, "auth_index": id, "provider": "workbuddy"})
		}
		return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"files": files}})
	case "host.auth.get":
		var req struct {
			AuthIndex string `json:"auth_index"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"json": json.RawMessage(h.raw(req.AuthIndex))}})
	case "host.auth.save":
		var req struct {
			Name string          `json:"name"`
			JSON json.RawMessage `json:"json"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.saves++
		if h.failSave {
			return nil, errors.New("isolated storage failure")
		}
		id := strings.TrimSuffix(req.Name, ".json")
		if _, exists := h.files[id]; !exists {
			return nil, errors.New("save changed credential identity")
		}
		h.files[id] = bytes.Clone(req.JSON)
		return []byte(`{"ok":true,"result":{}}`), nil
	case "host.log":
		h.mu.Lock()
		h.logs = append(h.logs, bytes.Clone(raw))
		h.mu.Unlock()
		return []byte(`{"ok":true,"result":{}}`), nil
	case "host.http.do":
		var req httpRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		if strings.HasSuffix(req.URL, "/v2/plugin/auth/token/refresh") {
			h.mu.Lock()
			h.refreshes = append(h.refreshes, req)
			h.mu.Unlock()
			if h.onRefresh != nil {
				return h.onRefresh(req)
			}
			return success(map[string]any{"accessToken": "access-next", "refreshToken": "refresh-next", "expiresIn": 3600, "refreshExpiresIn": 7200})
		}
		if strings.HasSuffix(req.URL, "/v3/config") {
			h.mu.Lock()
			h.configTokens = append(h.configTokens, strings.Join(req.Headers["Authorization"], ""))
			h.mu.Unlock()
			return success(json.RawMessage(`{"agents":[{"name":"cli","models":["common"]}],"models":[{"id":"common"}]}`))
		}
		return nil, errors.New("unexpected HTTP endpoint")
	case "host.http.do_stream":
		var req httpRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.opens = append(h.opens, req)
		id := fmt.Sprintf("rotation-stream-%d", len(h.opens))
		status := 200
		if h.rejectAll || (h.rejectOld && strings.Join(req.Headers["Authorization"], "") == "Bearer access-start") {
			status = 401
		}
		h.streamStatus[id] = status
		h.mu.Unlock()
		if h.onOpen != nil {
			h.onOpen()
		}
		return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": status, "stream_id": id}})
	case "host.http.stream_read":
		var req struct {
			StreamID string `json:"stream_id"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		h.mu.Lock()
		status := h.streamStatus[req.StreamID]
		h.mu.Unlock()
		payload := []byte("data: {\"id\":\"rotation\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		if status == 401 {
			payload = []byte(`{"error":{"message":"isolated rejection"}}`)
		}
		return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": payload, "done": true}})
	case "host.http.stream_close", "host.stream.emit", "host.stream.close":
		return []byte(`{"ok":true,"result":{}}`), nil
	default:
		return nil, fmt.Errorf("unexpected host callback %s", method)
	}
}

func refreshWire(auth workbuddy.AuthData) map[string]any {
	return map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Metadata": auth.Metadata, "host_callback_id": "rotation-context"}
}

func TestRefreshHeadersExpiryAndHostState(t *testing.T) {
	for _, site := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(site, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, site, "enterprise-one", 3*time.Minute)
			h := newRotationHost(t, auth)
			before := time.Now()
			result, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			if err != nil {
				t.Fatal(err)
			}
			got := result.(workbuddy.RefreshResponse)
			endpoint := "https://copilot.tencent.com/v2/plugin/auth/token/refresh"
			domain := "copilot.tencent.com"
			if site == "workbuddy-ai" {
				endpoint = "https://www.workbuddy.ai/v2/plugin/auth/token/refresh"
				domain = "www.workbuddy.ai"
			}
			req := h.refreshes[0]
			if req.Method != "POST" || req.URL != endpoint || req.CallbackID != "rotation-context" || string(req.Body) != "{}" {
				t.Fatalf("refresh endpoint/context/body=%+v", req)
			}
			for key, want := range map[string]string{"X-Refresh-Token": "refresh-start", "X-Auth-Refresh-Source": "plugin", "X-Domain": domain, "X-Enterprise-Id": "enterprise-one", "X-Tenant-Id": "enterprise-one"} {
				if strings.Join(req.Headers[key], "") != want {
					t.Errorf("header %s lost credential scope", key)
				}
			}
			if len(req.Headers["Authorization"]) != 0 {
				t.Error("refresh carried an expired access authorization")
			}
			if got.Auth.ID != auth.ID || got.Auth.FileName != auth.FileName || !got.Auth.Disabled || got.Auth.ProxyURL != "http://127.0.0.1:1234" || got.Auth.Metadata["priority"] != float64(7) {
				t.Fatal("rotation changed host identity/management")
			}
			if got.Auth.Metadata["access_token"] != "access-next" || got.Auth.Metadata["refresh_token"] != "refresh-next" {
				t.Fatal("rotated tokens were not installed")
			}
			if got.NextRefreshAfter.Before(before.Add(54*time.Minute)) || got.NextRefreshAfter.After(time.Now().Add(56*time.Minute)) || !got.NextRefreshAfter.Equal(got.Auth.NextRefreshAfter) {
				t.Fatal("seconds expiry or five-minute schedule was lost")
			}
			stored := parseRefreshAuth(t, h.raw(auth.ID))
			if stored.Metadata["access_token"] != "access-next" || !stored.Disabled || stored.ID != auth.ID {
				t.Fatal("persisted credentials lost rotation/state")
			}
		})
	}
}

func TestRefreshManualRequestAndDiscoveryShareConsumption(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := workbuddy.New()
		defer s.Close()
		auth := refreshIdentity(t, "workbuddy", "team", 30*time.Second)
		h := newRotationHost(t, auth)
		release := make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		h.onRefresh = func(httpRequest) ([]byte, error) {
			<-release
			return success(map[string]any{"accessToken": "access-next", "refreshToken": "refresh-next", "expiresIn": 3600})
		}
		results := make(chan error, 3)
		for _, method := range []string{"auth.refresh", "executor.execute", "model.for_auth"} {
			go func() {
				input := refreshWire(auth)
				if method == "executor.execute" {
					input = chatWireRequest(auth, false)
				}
				_, err := invoke(s, context.Background(), method, input, h.call)
				results <- err
			}()
		}
		synctest.Wait()
		if len(h.refreshes) != 1 {
			t.Fatalf("concurrent consumers spent token %d times", len(h.refreshes))
		}
		unblock()
		for range 3 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		if len(h.refreshes) != 1 || h.saves != 1 || len(h.opens) != 1 || len(h.configTokens) != 1 {
			t.Fatal("refresh fan-in did not share installed generation")
		}
		if strings.Join(h.opens[0].Headers["Authorization"], "") != "Bearer access-next" || h.configTokens[0] != "Bearer access-next" {
			t.Fatal("chat and model discovery did not use the refreshed generation")
		}
	})
}

func TestChatUnauthorizedRecoveryIsBoundedAndKeepsRequest(t *testing.T) {
	for _, rejectAll := range []bool{false, true} {
		t.Run(fmt.Sprint(rejectAll), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy-ai", "", time.Hour)
			h := newRotationHost(t, auth)
			h.rejectOld = true
			h.rejectAll = rejectAll
			_, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call)
			if (err != nil) != rejectAll {
				t.Fatalf("second rejection outcome=%v", err)
			}
			if len(h.opens) != 2 || len(h.refreshes) != 1 {
				t.Fatalf("auth recovery opens=%d refreshes=%d", len(h.opens), len(h.refreshes))
			}
			first, second := h.opens[0], h.opens[1]
			if strings.Join(first.Headers["Authorization"], "") != "Bearer access-start" || strings.Join(second.Headers["Authorization"], "") != "Bearer access-next" {
				t.Fatal("request did not switch tokens exactly once")
			}
			if !bytes.Equal(first.Body, second.Body) || strings.Join(first.Headers["X-Request-ID"], "") != strings.Join(second.Headers["X-Request-ID"], "") {
				t.Fatal("auth replay changed request body or turn identity")
			}
		})
	}
}

func TestRefreshAdoptsHostGenerationBeforeAndAfterNetwork(t *testing.T) {
	for _, stage := range []string{"401", "success", "rejected"} {
		t.Run(stage, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			h := newRotationHost(t, auth)
			if stage == "401" {
				h.rejectOld = true
				h.onOpen = func() {
					if err := h.replace(auth.ID, "access-theirs", "refresh-theirs"); err != nil {
						t.Error(err)
					}
				}
				if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
					t.Fatal(err)
				}
				if len(h.refreshes) != 0 || len(h.opens) != 2 || strings.Join(h.opens[1].Headers["Authorization"], "") != "Bearer access-theirs" {
					t.Fatal("401 spent a token already replaced in host")
				}
				return
			}
			h.onRefresh = func(httpRequest) ([]byte, error) {
				if err := h.replace(auth.ID, "access-theirs", "refresh-theirs"); err != nil {
					return nil, err
				}
				if stage == "rejected" {
					return response(401, map[string]any{"code": 12153})
				}
				return success(map[string]any{"accessToken": "access-late", "refreshToken": "refresh-late", "expiresIn": 3600})
			}
			result, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			if err != nil {
				t.Fatal(err)
			}
			if result.(workbuddy.RefreshResponse).Auth.Metadata["access_token"] != "access-theirs" || h.saves != 0 || parseRefreshAuth(t, h.raw(auth.ID)).Metadata["access_token"] != "access-theirs" {
				t.Fatal("late result overwrote externally installed generation")
			}
		})
	}
}

func TestRefreshSaveFailureKeepsMemoryAndCanPersistWithoutConsumption(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := workbuddy.New()
		defer s.Close()
		auth := refreshIdentity(t, "workbuddy", "", time.Hour)
		h := newRotationHost(t, auth)
		h.failSave = true
		_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
		detail, ok := errors.AsType[*workbuddy.Error](err)
		if !ok || detail.Code != "credential_save_failed" || !strings.Contains(detail.Message, "restarting") {
			t.Fatalf("persistence failure not observable: %v", err)
		}
		time.Sleep(61 * time.Second)
		if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
			t.Fatal(err)
		}
		if len(h.refreshes) != 1 || strings.Join(h.opens[0].Headers["Authorization"], "") != "Bearer access-next" {
			t.Fatal("save failure discarded memory rotation or consumed token again")
		}
		h.failSave = false
		if _, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call); err != nil {
			t.Fatal(err)
		}
		if len(h.refreshes) != 1 || parseRefreshAuth(t, h.raw(auth.ID)).Metadata["access_token"] != "access-next" {
			t.Fatal("persistence retry reconsumed old refresh token")
		}
		restarted := workbuddy.New()
		defer restarted.Close()
		restored := parseRefreshAuth(t, h.raw(auth.ID))
		if _, err := invoke(restarted, context.Background(), "auth.refresh", refreshWire(restored), h.call); err != nil {
			t.Fatal(err)
		}
		if len(h.refreshes) != 2 || strings.Join(h.refreshes[1].Headers["X-Refresh-Token"], "") != "refresh-next" {
			t.Fatal("restart did not recover persisted refresh generation")
		}
	})
}

func TestRefreshResultWindowAndIdentityIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := workbuddy.New()
		defer s.Close()
		auth := refreshIdentity(t, "workbuddy", "team-one", time.Hour)
		h := newRotationHost(t, auth)
		h.onRefresh = func(httpRequest) ([]byte, error) {
			return success(map[string]any{"accessToken": "access-next", "refreshToken": "refresh-start", "expiresIn": 3600})
		}
		if _, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call); err != nil {
			t.Fatal(err)
		}
		current := parseRefreshAuth(t, h.raw(auth.ID))
		time.Sleep(59 * time.Second)
		if _, err := invoke(s, context.Background(), "auth.refresh", refreshWire(current), h.call); err != nil {
			t.Fatal(err)
		}
		if len(h.refreshes) != 1 {
			t.Fatal("same refresh token result was not kept for sixty seconds")
		}
		time.Sleep(2 * time.Second)
		if _, err := invoke(s, context.Background(), "auth.refresh", refreshWire(current), h.call); err != nil {
			t.Fatal(err)
		}
		if len(h.refreshes) != 2 {
			t.Fatal("nonrotating refresh token stayed cached beyond its result window")
		}
		for _, other := range []workbuddy.AuthData{refreshIdentity(t, "workbuddy", "team-two", time.Hour), refreshIdentity(t, "workbuddy-ai", "team-one", time.Hour)} {
			otherHost := newRotationHost(t, other)
			if _, err := invoke(s, context.Background(), "auth.refresh", refreshWire(other), otherHost.call); err != nil {
				t.Fatal(err)
			}
			if len(otherHost.refreshes) != 1 {
				t.Fatal("refresh result crossed account identity/site scope")
			}
		}
	})
}

func TestRefreshFailuresKeepUnexpiredAccessAndReportReason(t *testing.T) {
	for _, mode := range []string{"network", "server", "timeout", "rejected", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", 30*time.Second)
			if mode == "expired" {
				fields := auth.Metadata
				fields["refresh_expires_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				raw, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				auth = parseRefreshAuth(t, raw)
			}
			h := newRotationHost(t, auth)
			h.onRefresh = func(httpRequest) ([]byte, error) {
				switch mode {
				case "network":
					return nil, errors.New("isolated network failure")
				case "server":
					return response(502, map[string]any{})
				case "timeout":
					return response(408, map[string]any{})
				default:
					return response(401, map[string]any{"code": 12153})
				}
			}
			if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
				t.Fatal(err)
			}
			if len(h.opens) != 1 || strings.Join(h.opens[0].Headers["Authorization"], "") != "Bearer access-start" || len(h.logs) != 1 {
				t.Fatal("temporary refresh failure discarded unexpired access or went unreported")
			}
			if bytes.Contains(h.logs[0], []byte("access-start")) || bytes.Contains(h.logs[0], []byte("refresh-start")) {
				t.Fatal("refresh diagnostic exposed tokens")
			}
			if mode == "expired" && len(h.refreshes) != 0 {
				t.Fatal("expired refresh token was consumed")
			}
		})
	}
}

func TestRefreshAbsoluteExpiryAndRequestWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := workbuddy.New()
		defer s.Close()
		auth := refreshIdentity(t, "workbuddy-ai", "", 3*time.Minute)
		h := newRotationHost(t, auth)
		if _, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), h.call); err != nil {
			t.Fatal(err)
		}
		if len(h.refreshes) != 0 {
			t.Fatal("request used the five-minute host refresh window")
		}
		h.onRefresh = func(httpRequest) ([]byte, error) {
			return success(map[string]any{"accessToken": "access-absolute", "expiresAt": int64(1893456000000), "refreshExpiresAt": int64(1924992000000)})
		}
		result, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
		if err != nil {
			t.Fatal(err)
		}
		got := result.(workbuddy.RefreshResponse)
		if got.NextRefreshAfter.Format(time.RFC3339) != "2029-12-31T23:55:00Z" || got.Auth.Metadata["expires_at"] != "2030-01-01T00:00:00Z" || got.Auth.Metadata["refresh_expires_at"] != "2031-01-01T00:00:00Z" || got.Auth.Metadata["refresh_token"] != "refresh-start" {
			t.Fatal("absolute millisecond expiry or omitted rotating fields were not preserved")
		}
	})
}

func TestRefreshCancelledWaiterDoesNotConsumeGeneration(t *testing.T) {
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
			<-release
			return success(map[string]any{"accessToken": "access-next", "refreshToken": "refresh-next", "expiresIn": 3600})
		}
		first := make(chan error, 1)
		go func() {
			_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
			first <- err
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		waiter := make(chan error, 1)
		go func() { _, err := invoke(s, ctx, "auth.refresh", refreshWire(auth), h.call); waiter <- err }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		err := <-waiter
		if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.Code != "request_cancelled" {
			t.Fatalf("cancelled waiter=%v", err)
		}
		unblock()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if len(h.refreshes) != 1 {
			t.Fatal("cancelled waiter spent the refresh token")
		}
	})
}

func TestChatHTTPStatusAndRecoverySurviveCleanupFailure(t *testing.T) {
	for _, status := range []int{400, 401} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			h := newRotationHost(t, auth)
			h.rejectOld = status == 401
			call := func(method string, raw []byte) ([]byte, error) {
				if status == 400 && method == "host.http.do_stream" {
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": 400, "stream_id": "bad-request"}})
				}
				if method == "host.http.stream_close" {
					var ref struct {
						StreamID string `json:"stream_id"`
					}
					if err := json.Unmarshal(raw, &ref); err != nil {
						return nil, err
					}
					if ref.StreamID == "bad-request" || ref.StreamID == "rotation-stream-1" {
						return nil, errors.New("isolated cleanup failure")
					}
				}
				return h.call(method, raw)
			}
			_, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(auth, false), call)
			if status == 401 {
				if err != nil || len(h.opens) != 2 || len(h.refreshes) != 1 {
					t.Fatalf("cleanup blocked one401recovery: %v", err)
				}
			} else if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.HTTPStatus != 400 {
				t.Fatalf("cleanup replaced original HTTP400: %v", err)
			}
			if len(h.logs) != 1 {
				t.Fatal("cleanup error was not reported through host logging")
			}
		})
	}
}

func TestRefreshMissingExpiryIsRejectedBeforeInstallation(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := refreshIdentity(t, "workbuddy", "", 30*time.Second)
	h := newRotationHost(t, auth)
	h.onRefresh = func(httpRequest) ([]byte, error) {
		return success(map[string]any{"accessToken": "access-incomplete", "refreshToken": "refresh-incomplete"})
	}
	_, err := invoke(s, context.Background(), "auth.refresh", refreshWire(auth), h.call)
	if detail, ok := errors.AsType[*workbuddy.Error](err); !ok || detail.Code != "invalid_refresh_response" {
		t.Fatalf("missing expiry accepted: %v", err)
	}
	if h.saves != 0 || parseRefreshAuth(t, h.raw(auth.ID)).Metadata["access_token"] != "access-start" {
		t.Fatal("invalid refresh installed a past plan")
	}
}
