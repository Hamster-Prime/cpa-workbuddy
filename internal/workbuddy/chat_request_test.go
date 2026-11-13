package workbuddy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"cpa-workbuddy/internal/workbuddy"
)

func requestProtocolInput(auth workbuddy.AuthData, model string, payload []byte) map[string]any {
	return map[string]any{
		"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON,
		"Model": model, "Format": "openai", "SourceFormat": "openai", "Payload": payload,
		"host_callback_id": "chat-request-callback",
	}
}

func requestProtocolHost(t *testing.T, observe func(httpRequest)) hostCall {
	t.Helper()
	read := false
	return func(method string, raw []byte) ([]byte, error) {
		switch method {
		case "host.http.do_stream":
			var req httpRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
			observe(req)
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{
				"status_code": 200, "headers": map[string][]string{"Content-Type": {"text/event-stream"}}, "stream_id": "request-upstream-stream",
			}})
		case "host.http.stream_read":
			if read {
				return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"done": true}})
			}
			read = true
			payload := []byte("data: {\"id\":\"chat-text\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": payload, "done": false}})
		case "host.http.stream_close":
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			t.Errorf("unexpected chat request callback %q", method)
			return nil, errors.New("unexpected callback")
		}
	}
}

func requestProtocolBody(t *testing.T, req httpRequest) map[string]json.RawMessage {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(req.Body))
	if err != nil {
		t.Fatalf("upstream request is not gzip: %v", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("upstream body is not JSON: %v", err)
	}
	return body
}

func executeRequestProtocol(t *testing.T, s *workbuddy.Service, req map[string]any) httpRequest {
	t.Helper()
	var sent httpRequest
	calls := 0
	_, err := invoke(s, context.Background(), "executor.execute", req, requestProtocolHost(t, func(upstream httpRequest) {
		calls++
		sent = upstream
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 1 {
		t.Fatalf("upstream requests = %d, want 1", calls)
	}
	return sent
}

func TestChatRequestUsesCredentialSiteAndResolvedModel(t *testing.T) {
	for _, tc := range []struct {
		site, model, upstreamModel, endpoint, agent string
	}{
		{"workbuddy", "wb/glm-5.3", "glm-5.3", "https://copilot.tencent.com/v2/chat/completions", "WorkBuddy/5.9.3 WorkBuddy/5.9.3 CLI/2.165.0"},
		{"workbuddy-ai", "wb-ai/custom-organization/deployed-model-2026", "custom-organization/deployed-model-2026", "https://www.workbuddy.ai/v2/chat/completions", "WorkBuddy/5.9.3 workbuddy-ai/5.9.3 CLI/2.165.0"},
		{"workbuddy-ai", "wb-ai/claude-sonnet-4", "claude-sonnet-4", "https://www.workbuddy.ai/v2/chat/completions", "WorkBuddy/5.9.3 workbuddy-ai/5.9.3 CLI/2.165.0"},
	} {
		t.Run(tc.site+"/"+tc.model, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, tc.site, "team")
			req := requestProtocolInput(auth, tc.model, []byte(`{"model":"client-alias","messages":[{"role":"developer","content":"Follow directions."},{"role":"user","content":[{"type":"text","text":"Hello "},{"type":"text","text":"世界"}]}],"max_completion_tokens":100,"temperature":0.25,"stop":["END"],"store":false,"seed":9007199254740993,"stream_options":{"include_usage":false,"extra":"kept"}}`))
			req["Headers"] = map[string][]string{
				"Authorization": {"Bearer attacker"}, "X-Enterprise-Id": {"other-team"}, "X-Tenant-Id": {"other-team"},
				"X-User-Id": {"other-user"}, "User-Agent": {"attacker"}, "Content-Encoding": {"br"},
			}
			req["AuthMetadata"] = map[string]any{"site": "other", "endpoint": "https://other.invalid", "enterprise_id": "other-team"}
			sent := executeRequestProtocol(t, s, req)
			if sent.URL != tc.endpoint || sent.Method != "POST" || sent.CallbackID != "chat-request-callback" {
				t.Fatalf("upstream routing = %s %s callback %q", sent.Method, sent.URL, sent.CallbackID)
			}
			for key, want := range map[string]string{
				"Authorization": "Bearer fixture-browser-access", "X-User-Id": "person", "X-Enterprise-Id": "team", "X-Tenant-Id": "team",
				"User-Agent": tc.agent, "Content-Type": "application/json", "Content-Encoding": "gzip", "Accept": "application/json",
				"X-Agent-Type": "main", "X-Agent-Runtime": "local-agent", "X-Stainless-Retry-Count": "0", "X-Stainless-Timeout": "1200",
				"X-Stainless-Lang": "js", "X-Stainless-Package-Version": "6.25.0", "X-Stainless-Runtime": "node", "X-Stainless-Runtime-Version": "v22.21.1",
				"X-Product": "SaaS", "X-IDE-Type": "WorkBuddy", "X-Agent-Intent": "craft", "X-Agent-Purpose": "conversation", "X-IDE-Name": "WorkBuddy", "X-IDE-Version": "5.9.3",
			} {
				if got := sent.Headers[key]; len(got) != 1 || got[0] != want {
					t.Errorf("header %s = %v, want %q", key, got, want)
				}
			}
			osName := map[string]string{"windows": "Windows", "linux": "Linux", "darwin": "MacOS", "freebsd": "FreeBSD", "openbsd": "OpenBSD", "android": "Android"}[runtime.GOOS]
			if osName == "" {
				osName = "Other:" + runtime.GOOS
			}
			arch := map[string]string{"amd64": "x64", "arm64": "arm64", "arm": "arm", "386": "x32"}[runtime.GOARCH]
			if arch == "" {
				arch = "other:" + runtime.GOARCH
			}
			if sent.Headers["X-Stainless-OS"][0] != osName || sent.Headers["X-Stainless-Arch"][0] != arch {
				t.Error("SDK runtime attribution differs from actual OS and architecture")
			}
			body := requestProtocolBody(t, sent)
			for key, want := range map[string]string{
				"model":      `"` + tc.upstreamModel + `"`,
				"max_tokens": "100", "temperature": "0.25", "stop": `["END"]`, "store": "false", "seed": "9007199254740993", "stream": "true",
				"stream_options": `{"extra":"kept","include_usage":true}`,
				"messages":       `[{"content":"Follow directions.","role":"system"},{"content":[{"type":"text","text":"Hello "},{"type":"text","text":"世界"}],"role":"user"}]`,
			} {
				if got := string(body[key]); got != want {
					t.Errorf("body %s = %s, want %s", key, got, want)
				}
			}
			if _, exists := body["max_completion_tokens"]; exists {
				t.Error("legacy output token field was retained")
			}
		})
	}
}

func TestChatRequestDefaultsSystemAndKeepsExplicitTokenLimit(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	for _, tc := range []struct{ payload, messages string }{
		{`{"messages":[{"role":"user","content":"hi"}],"max_tokens":64,"max_completion_tokens":128}`, `[{"content":"You are a helpful assistant.","role":"system"},{"content":"hi","role":"user"}]`},
		{`{"messages":[{"role":"system","content":"Existing system."},{"role":"user","content":"hi"}],"max_tokens":64}`, `[{"content":"Existing system.","role":"system"},{"content":"hi","role":"user"}]`},
	} {
		sent := executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/glm-5.3", []byte(tc.payload)))
		body := requestProtocolBody(t, sent)
		if string(body["messages"]) != tc.messages || string(body["max_tokens"]) != "64" || body["max_completion_tokens"] != nil || string(body["stream_options"]) != `{"include_usage":true}` {
			t.Fatalf("normalization = %s", mustRequestProtocolJSON(t, body))
		}
	}
}

func mustRequestProtocolJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestChatRequestRejectsUnsupportedAndInvalidInputBeforeHTTP(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	for _, tc := range []struct {
		name, model, payload, code string
	}{
		{"unprefixed", "glm-5.3", `{"messages":[{"role":"user","content":"hi"}]}`, "unsupported_model"},
		{"cross-site", "wb-ai/glm-5.3", `{"messages":[{"role":"user","content":"hi"}]}`, "unsupported_model"},
		{"nested-prefix", "wb/wb/glm-5.3", `{"messages":[{"role":"user","content":"hi"}]}`, "unsupported_model"},
		{"nested-other-site", "wb/wb-ai/glm-5.3", `{"messages":[{"role":"user","content":"hi"}]}`, "unsupported_model"},
		{"empty-model", "wb/", `{"messages":[{"role":"user","content":"hi"}]}`, "unsupported_model"},
		{"claude-messages", "wb/ClAuDe-sonnet-4", `{"messages":[{"role":"user","content":"hi"}]}`, "unsupported_model"},
		{"array-payload", "wb/glm-5.3", `[]`, "invalid_request"},
		{"null-payload", "wb/glm-5.3", `null`, "invalid_request"},
		{"trailing-json", "wb/glm-5.3", `{"messages":[]} {}`, "invalid_request"},
		{"missing-messages", "wb/glm-5.3", `{}`, "invalid_request"},
		{"empty-messages", "wb/glm-5.3", `{"messages":[]}`, "invalid_request"},
		{"null-message", "wb/glm-5.3", `{"messages":[null]}`, "invalid_request"},
		{"null-text", "wb/glm-5.3", `{"messages":[{"role":"user","content":null}]}`, "invalid_request"},
		{"null-text-part", "wb/glm-5.3", `{"messages":[{"role":"user","content":[{"type":"text","text":null}]}]}`, "invalid_request"},
		{"unknown-role", "wb/glm-5.3", `{"messages":[{"role":"other","content":"hi"}]}`, "invalid_request"},
		{"bad-stream-options", "wb/glm-5.3", `{"messages":[{"role":"user","content":"hi"}],"stream_options":[]}`, "invalid_request"},
		{"tools", "wb/glm-5.3", `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f"}}]}`, "unsupported_feature"},
		{"tool-choice", "wb/glm-5.3", `{"messages":[{"role":"user","content":"hi"}],"tool_choice":"auto"}`, "unsupported_feature"},
		{"legacy-function-history", "wb/glm-5.3", `{"messages":[{"role":"function","content":"result"}]}`, "unsupported_feature"},
		{"image-input", "wb/glm-5.3", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/image"}}]}]}`, "unsupported_feature"},
		{"reasoning-effort", "wb/glm-5.3", `{"messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high"}`, "unsupported_feature"},
		{"image-generation", "wb/glm-5.3", `{"messages":[{"role":"user","content":"hi"}],"generate":true}`, "unsupported_feature"},
		{"audio-output", "wb/glm-5.3", `{"messages":[{"role":"user","content":"hi"}],"modalities":["text","audio"]}`, "unsupported_feature"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, tc.model, []byte(tc.payload)), func(string, []byte) ([]byte, error) {
				t.Error("rejected chat reached host HTTP")
				return nil, errors.New("unexpected HTTP")
			})
			var safe *workbuddy.Error
			if !errors.As(err, &safe) || safe.Code != tc.code {
				t.Fatalf("error = %v, want code %s", err, tc.code)
			}
		})
	}
	for _, field := range []string{"Format", "AuthID", "AuthProvider"} {
		t.Run(field, func(t *testing.T) {
			req := requestProtocolInput(auth, "wb/glm-5.3", []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
			req[field] = "other"
			_, err := invoke(s, context.Background(), "executor.execute", req, func(string, []byte) ([]byte, error) {
				t.Error("invalid format or account reached HTTP")
				return nil, errors.New("unexpected HTTP")
			})
			if err == nil {
				t.Fatal("invalid format or account was accepted")
			}
		})
	}
}

func TestChatRequestConversationIDsFollowTurnsSessionsAndAccounts(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	personal := parsedIdentity(t, "workbuddy", "")
	enterprise := parsedIdentity(t, "workbuddy", "team")
	international := parsedIdentity(t, "workbuddy-ai", "")
	first := []byte(`{"messages":[{"role":"user","content":"first"}]}`)
	later := []byte(`{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"answer"},{"role":"user","content":"second"}]}`)
	ask := func(auth workbuddy.AuthData, body []byte, metadata map[string]any, headers map[string][]string) map[string][]string {
		t.Helper()
		model := "wb/glm-5.3"
		if auth.ID == international.ID {
			model = "wb-ai/glm-5.3"
		}
		req := requestProtocolInput(auth, model, body)
		req["Metadata"], req["Headers"] = metadata, headers
		return executeRequestProtocol(t, s, req).Headers
	}
	one := ask(personal, first, nil, nil)
	again := ask(personal, first, nil, nil)
	next := ask(personal, later, nil, nil)
	for _, equivalent := range []string{
		`{"messages":[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"first"}]}`,
		`{"messages":[{"role":"user","content":[{"type":"text","text":"fi"},{"type":"text","text":"rst"}]}]}`,
	} {
		equal := ask(personal, []byte(equivalent), nil, nil)
		if one["X-Conversation-ID"][0] != equal["X-Conversation-ID"][0] || one["X-Conversation-Request-ID"][0] != equal["X-Conversation-Request-ID"][0] {
			t.Error("equivalent normalized text changed the conversation or turn")
		}
	}
	lcp := ask(personal, first, map[string]any{"lcp_affinity_session_id": "routing-only", "session_id": "unrecognized-host-key"}, nil)
	if lcp["X-Conversation-ID"][0] != one["X-Conversation-ID"][0] {
		t.Error("routing-only or unrecognized metadata was used as a provider conversation")
	}
	for _, key := range []string{"execution_session_id", "derived_session_id"} {
		fromMetadata := ask(personal, first, map[string]any{key: "session-fallback"}, nil)
		fromHeader := ask(personal, first, nil, map[string][]string{"x-magpie-workbuddy-session": {"session-fallback"}})
		if fromMetadata["X-Conversation-ID"][0] != fromHeader["X-Conversation-ID"][0] || fromMetadata["X-Conversation-Request-ID"][0] != fromHeader["X-Conversation-Request-ID"][0] {
			t.Errorf("CPA %s did not use the source session identity", key)
		}
	}
	for _, continuation := range []string{
		`{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"read","arguments":"{}"}}]}]}`,
		`{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-1","content":"result"}]}`,
		`{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call-1","content":"result"},{"role":"system","content":"turn metadata"},{"role":"user","content":"attached tool text"}]}`,
	} {
		continued := ask(personal, []byte(continuation), nil, nil)
		if one["X-Conversation-ID"][0] != continued["X-Conversation-ID"][0] || one["X-Conversation-Request-ID"][0] != continued["X-Conversation-Request-ID"][0] {
			t.Error("historical tool continuation changed the original user's conversation or turn")
		}
	}
	for _, key := range []string{"X-Conversation-ID", "X-Conversation-Request-ID", "X-Conversation-Message-ID", "X-Request-ID"} {
		value := one[key][0]
		if decoded, err := hex.DecodeString(value); err != nil || len(decoded) != 16 {
			t.Errorf("%s is not a 32-digit hex ID: %q", key, value)
		}
	}
	if one["X-Conversation-ID"][0] != again["X-Conversation-ID"][0] || one["X-Conversation-Request-ID"][0] != again["X-Conversation-Request-ID"][0] {
		t.Error("a retried user send changed conversation or turn ID")
	}
	if one["X-Conversation-ID"][0] != next["X-Conversation-ID"][0] || one["X-Conversation-Request-ID"][0] == next["X-Conversation-Request-ID"][0] {
		t.Error("the next user send did not retain conversation and change turn")
	}
	if one["X-Conversation-Message-ID"][0] == again["X-Conversation-Message-ID"][0] || one["X-Conversation-Message-ID"][0] != one["X-Request-ID"][0] {
		t.Error("message IDs are not fresh per HTTP request or differ from request IDs")
	}
	for _, auth := range []workbuddy.AuthData{enterprise, international} {
		other := ask(auth, first, nil, nil)
		if one["X-Conversation-ID"][0] == other["X-Conversation-ID"][0] || one["X-Conversation-Request-ID"][0] == other["X-Conversation-Request-ID"][0] {
			t.Error("conversation or turn ID crossed account/site scope")
		}
	}
	meta := map[string]any{"canonical_session_id": "session-one", "execution_session_id": "ignored-session"}
	session := ask(personal, first, meta, map[string][]string{"x-magpie-workbuddy-session": {"ignored-header"}})
	sessionNext := ask(personal, later, meta, nil)
	if session["X-Conversation-ID"][0] != sessionNext["X-Conversation-ID"][0] || session["X-Conversation-Request-ID"][0] == sessionNext["X-Conversation-Request-ID"][0] {
		t.Error("CPA canonical session did not join turns")
	}
	otherSession := ask(personal, first, map[string]any{"canonical_session_id": "session-two"}, nil)
	if session["X-Conversation-ID"][0] == otherSession["X-Conversation-ID"][0] || session["X-Conversation-Request-ID"][0] == otherSession["X-Conversation-Request-ID"][0] {
		t.Error("identical user text crossed CPA sessions")
	}
	fallback := ask(personal, first, nil, map[string][]string{"x-magpie-workbuddy-session": {"session-one"}})
	if session["X-Conversation-ID"][0] != fallback["X-Conversation-ID"][0] {
		t.Error("source session header and CPA session disagree")
	}
	if len(fallback["x-magpie-workbuddy-session"]) != 0 || len(fallback["X-Magpie-Workbuddy-Session"]) != 0 {
		t.Error("private session header escaped upstream")
	}
	longSession := strings.Repeat("s", 128)
	capped := ask(personal, first, map[string]any{"canonical_session_id": longSession + "extra"}, nil)
	exact := ask(personal, first, map[string]any{"canonical_session_id": longSession}, nil)
	if capped["X-Conversation-ID"][0] != exact["X-Conversation-ID"][0] {
		t.Error("session identity did not use the 128-character cap")
	}
	explicit := map[string][]string{"x-conversation-id": {"caller-conversation"}, "x-conversation-request-id": {"caller-turn"}, "X-Request-ID": {"caller-message"}}
	explicitOne := ask(personal, first, nil, explicit)
	explicitOther := ask(enterprise, first, nil, explicit)
	if explicitOne["X-Conversation-ID"][0] == "caller-conversation" || explicitOne["X-Conversation-Request-ID"][0] == "caller-turn" || explicitOne["X-Request-ID"][0] == "caller-message" {
		t.Error("caller IDs escaped account scoping or replaced fresh message IDs")
	}
	if explicitOne["X-Conversation-ID"][0] == explicitOther["X-Conversation-ID"][0] || explicitOne["X-Conversation-Request-ID"][0] == explicitOther["X-Conversation-Request-ID"][0] {
		t.Error("explicit IDs crossed account scope")
	}
	onlySystem := []byte(`{"messages":[{"role":"system","content":"instructions"}]}`)
	noUser := ask(personal, onlySystem, nil, nil)
	noUserAgain := ask(personal, onlySystem, nil, nil)
	if len(noUser["X-Conversation-ID"]) != 0 || noUser["X-Conversation-Request-ID"][0] == noUserAgain["X-Conversation-Request-ID"][0] {
		t.Error("a chat without user text invented a conversation or reused its random turn")
	}
}

func TestChatRequestPreservesToolHistoryAndNormalizesReasoningHistory(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	payload := []byte(`{"messages":[{"role":"user","content":"read it"},{"role":"assistant","content":null,"reasoning_content":"historical thought","tool_calls":[{"id":"call-1","type":"function","function":{"name":"fs.read","arguments":"{\"path\":\"a.txt\"}"}}]},{"role":"tool","tool_call_id":"call-1","content":[{"type":"text","text":"file contents"}]}],"tool_choice":"none"}`)
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/glm-5.3", payload)))
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(body["messages"], &messages); err != nil || len(messages) != 4 {
		t.Fatalf("historical messages = %s", body["messages"])
	}
	if string(messages[2]["tool_calls"]) != `[{"id":"call-1","type":"function","function":{"name":"fs.read","arguments":"{\"path\":\"a.txt\"}"}}]` || string(messages[2]["content"]) != "null" {
		t.Error("historical call ID, name, arguments, or null content changed")
	}
	if string(messages[2]["reasoning"]) != `"historical thought"` || messages[2]["reasoning_content"] != nil || messages[2]["reasoning_details"] != nil {
		t.Error("assistant historical reasoning did not use the source reasoning field")
	}
	if string(messages[3]["tool_call_id"]) != `"call-1"` || string(messages[3]["content"]) != `[{"type":"text","text":"file contents"}]` {
		t.Error("historical tool result changed")
	}
}

func TestChatRequestPreservesFullTextAndBoundsRawPayload(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	text := strings.Repeat("中🙂long-text", 100000) + "TAIL-MUST-SURVIVE"
	payload := []byte(mustRequestProtocolJSON(t, map[string]any{"messages": []map[string]string{{"role": "user", "content": text}}}))
	sent := executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/glm-5.3", payload))
	body := requestProtocolBody(t, sent)
	var messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body["messages"], &messages); err != nil || len(messages) != 2 || messages[1].Content != text {
		t.Fatal("bounded request text was truncated or changed")
	}
	oversized := bytes.Repeat([]byte(" "), (32<<20)+1)
	_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb/glm-5.3", oversized), func(string, []byte) ([]byte, error) {
		t.Error("oversized payload reached host HTTP")
		return nil, errors.New("unexpected HTTP")
	})
	var safe *workbuddy.Error
	if !errors.As(err, &safe) || safe.Code != "request_too_large" || safe.HTTPStatus != 413 {
		t.Fatalf("oversized payload error = %v", err)
	}
}

func TestChatRequestToolDisableFormsProduceTextRequests(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	for _, selection := range []map[string]any{
		{"tool_choice": "none", "tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "disabled"}}}},
		{"tool_choice": map[string]string{"type": "none"}, "functions": []map[string]string{{"name": "disabled"}}},
		{"function_call": "none", "functions": []map[string]string{{"name": "disabled"}}},
	} {
		selection["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
		payload := []byte(mustRequestProtocolJSON(t, selection))
		body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/common", payload)))
		for _, field := range []string{"tool_choice", "function_call", "tools", "functions"} {
			if _, exists := body[field]; exists {
				t.Fatalf("disabled tool field %s reached upstream", field)
			}
		}
	}
}
