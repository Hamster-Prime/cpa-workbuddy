package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

// These are wire expectations, including the exact position of each correction.
const recoveryOriginal = `[{"role":"system","content":"You are an AI agent powered by Example Harness."},{"role":"user","content":"Read the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"user","content":"Continue."}]`
const recoveryComma = `[{"role":"system","content":"You are an AI agent, powered by Example Harness."},{"role":"user","content":"Read the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"user","content":"Continue."}]`
const recoverySystem = `[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"<instructions>\nYou are an AI agent, powered by Example Harness.\n</instructions>\n\nRead the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"user","content":"Continue."}]`
const recoveryAfter = `[{"role":"system","content":"You are an AI agent powered by Example Harness."},{"role":"user","content":"Read the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"assistant","content":""},{"role":"user","content":"Continue."}]`
const recoveryCommaAfter = `[{"role":"system","content":"You are an AI agent, powered by Example Harness."},{"role":"user","content":"Read the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"assistant","content":""},{"role":"user","content":"Continue."}]`
const recoverySystemAfter = `[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"<instructions>\nYou are an AI agent, powered by Example Harness.\n</instructions>\n\nRead the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"assistant","content":""},{"role":"user","content":"Continue."}]`
const recoveryRememberedSystem = `[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"<instructions>\nYou are an AI agent powered by Example Harness.\n</instructions>\n\nRead the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"user","content":"Continue."}]`

// A script value is HTTP 200/401/429/5xx, or an application code in HTTP 400.
// The host uses the same public HTTP envelopes and credential fixture as the
// compatibility and refresh suites, while observing stream ownership as well.
type chatRecoveryHost struct {
	t         *testing.T
	rotation  *rotationHost
	script    []int
	opens     []httpRequest
	closed    map[string]int
	onOpen    func(int)
	errorBody func(int) []byte
}

func newChatRecoveryHost(t *testing.T, script ...int) *chatRecoveryHost {
	t.Helper()
	return &chatRecoveryHost{t: t, script: script, closed: make(map[string]int)}
}

func (h *chatRecoveryHost) call(method string, raw []byte) ([]byte, error) {
	switch method {
	case "host.http.do_stream":
		var request httpRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		index := len(h.opens)
		h.opens = append(h.opens, request)
		if index >= len(h.script) {
			h.t.Errorf("unexpected replay %d beyond response script %v", index+1, h.script)
			return nil, errors.New("recovery replay exceeded script")
		}
		if index > 0 && h.closed[fmt.Sprintf("recovery-%d", index-1)] != 1 {
			h.t.Error("recovery opened a replacement before closing its predecessor")
		}
		if h.onOpen != nil {
			h.onOpen(index)
		}
		status := h.script[index]
		if status > 599 {
			status = 400
		}
		headers := map[string][]string{}
		if status == 401 {
			headers["WWW-Authenticate"] = []string{`Bearer error="invalid_token"`}
		}
		if status == 429 || status >= 500 {
			headers["Retry-After"] = []string{"17"}
		}
		return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": status, "headers": headers, "stream_id": fmt.Sprintf("recovery-%d", index)}})
	case "host.http.stream_read", "host.http.stream_close":
		var ref struct {
			StreamID string `json:"stream_id"`
		}
		if err := json.Unmarshal(raw, &ref); err != nil {
			return nil, err
		}
		index := -1
		for i := range h.opens {
			if ref.StreamID == fmt.Sprintf("recovery-%d", i) {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("unknown recovery stream %q", ref.StreamID)
		}
		if method == "host.http.stream_close" {
			h.closed[ref.StreamID]++
			return []byte(`{"ok":true,"result":{}}`), nil
		}
		if h.closed[ref.StreamID] != 0 {
			h.t.Error("read from a closed recovery stream")
		}
		code := h.script[index]
		payload := []byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		if code != 200 {
			message := "upstream request failed"
			if code == 11128 {
				message = "Illegal API invocation from an unapproved channel"
			}
			if code == 11153 {
				message = "conversation must end with a user message"
			}
			payload, _ = json.Marshal(map[string]any{"code": code, "msg": message})
			if h.errorBody != nil {
				payload = h.errorBody(code)
			}
		}
		return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": payload, "done": true}})
	case "host.log":
		return []byte(`{"ok":true,"result":{}}`), nil
	default:
		if h.rotation != nil {
			return h.rotation.call(method, raw)
		}
		return nil, fmt.Errorf("unexpected recovery callback %q", method)
	}
}

func (h *chatRecoveryHost) execute(s *workbuddy.Service, req map[string]any, wantMessages ...string) error {
	h.t.Helper()
	_, err := invoke(s, context.Background(), "executor.execute", req, h.call)
	if len(h.opens) != len(wantMessages) {
		h.t.Fatalf("recovery opens = %d, want %d; error = %v", len(h.opens), len(wantMessages), err)
	}
	for i, request := range h.opens {
		if h.closed[fmt.Sprintf("recovery-%d", i)] != 1 {
			h.t.Errorf("attempt %d upstream closes = %d, want 1", i+1, h.closed[fmt.Sprintf("recovery-%d", i)])
		}
		compatibilityJSON(h.t, requestProtocolBody(h.t, request)["messages"], wantMessages[i])
		for _, header := range []string{"X-Conversation-ID", "X-Conversation-Request-ID", "X-Conversation-Message-ID", "X-Request-ID"} {
			value := strings.Join(request.Headers[header], "")
			if value == "" || value != strings.Join(h.opens[0].Headers[header], "") {
				h.t.Errorf("attempt %d changed or omitted %s", i+1, header)
			}
		}
		if request.CallbackID != "chat-request-callback" {
			h.t.Errorf("attempt %d changed callback context", i+1)
		}
	}
	return err
}

func recoveryInput(auth workbuddy.AuthData, model, messages string) map[string]any {
	return requestProtocolInput(auth, model, []byte(`{"messages":`+messages+`}`))
}

func TestChatRecoveryEachCorrectionIsAttemptedOnce(t *testing.T) {
	const plain = `[{"role":"system","content":"Example fingerprint"},{"role":"user","content":"hello"}]`
	const moved = `[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"<instructions>\nExample fingerprint\n</instructions>\n\nhello"}]`
	for _, tc := range []struct {
		name     string
		script   []int
		messages []string
	}{
		{"comma-then-system", []int{11128, 11128, 11128}, []string{recoveryOriginal, recoveryComma, recoverySystem}},
		{"system-without-comma-change", []int{11128, 11128}, []string{plain, moved}},
		{"aftertools", []int{11153, 11153}, []string{recoveryOriginal, recoveryAfter}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			h := newChatRecoveryHost(t, tc.script...)
			if err := h.execute(s, recoveryInput(parsedIdentity(t, "workbuddy", ""), "wb/common", tc.messages[0]), tc.messages...); err == nil {
				t.Fatal("repeated upstream refusal became success")
			}
			pristine := newChatRecoveryHost(t, 200)
			if err := pristine.execute(s, recoveryInput(parsedIdentity(t, "workbuddy", ""), "wb/common", tc.messages[0]), tc.messages[0]); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatCorrectionsReturnAuthenticationFailuresToManager(t *testing.T) {
	// Authentication rejection at any correction stage belongs to the host.
	for _, tc := range []struct {
		codes []int
		want  []string
	}{
		{[]int{401, 11128, 11128, 11153}, []string{recoveryOriginal, recoveryOriginal, recoveryComma, recoverySystem, recoverySystemAfter}},
		{[]int{11128, 401, 11128, 11153}, []string{recoveryOriginal, recoveryComma, recoveryComma, recoverySystem, recoverySystemAfter}},
		{[]int{11128, 11128, 401, 11153}, []string{recoveryOriginal, recoveryComma, recoverySystem, recoverySystem, recoverySystemAfter}},
		{[]int{11128, 11128, 11153, 401}, []string{recoveryOriginal, recoveryComma, recoverySystem, recoverySystemAfter, recoverySystemAfter}},
		{[]int{401, 11128, 11153, 11128}, []string{recoveryOriginal, recoveryOriginal, recoveryComma, recoveryCommaAfter, recoverySystemAfter}},
		{[]int{11128, 401, 11153, 11128}, []string{recoveryOriginal, recoveryComma, recoveryComma, recoveryCommaAfter, recoverySystemAfter}},
		{[]int{11128, 11153, 401, 11128}, []string{recoveryOriginal, recoveryComma, recoveryCommaAfter, recoveryCommaAfter, recoverySystemAfter}},
		{[]int{11128, 11153, 11128, 401}, []string{recoveryOriginal, recoveryComma, recoveryCommaAfter, recoverySystemAfter, recoverySystemAfter}},
		{[]int{401, 11153, 11128, 11128}, []string{recoveryOriginal, recoveryOriginal, recoveryAfter, recoveryCommaAfter, recoverySystemAfter}},
		{[]int{11153, 401, 11128, 11128}, []string{recoveryOriginal, recoveryAfter, recoveryAfter, recoveryCommaAfter, recoverySystemAfter}},
		{[]int{11153, 11128, 401, 11128}, []string{recoveryOriginal, recoveryAfter, recoveryCommaAfter, recoveryCommaAfter, recoverySystemAfter}},
		{[]int{11153, 11128, 11128, 401}, []string{recoveryOriginal, recoveryAfter, recoveryCommaAfter, recoverySystemAfter, recoverySystemAfter}},
	} {
		t.Run(fmt.Sprint(tc.codes), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy-ai", "", time.Hour)
			h := newChatRecoveryHost(t, append(tc.codes, 200)...)
			h.rotation = newRotationHost(t, auth)
			count := 1
			for _, code := range tc.codes {
				if code == 401 {
					break
				}
				count++
			}
			err := h.execute(s, recoveryInput(auth, "wb-ai/common", recoveryOriginal), tc.want[:count]...)
			var detail *workbuddy.Error
			if !errors.As(err, &detail) || detail.HTTPStatus != 401 || len(h.rotation.refreshes) != 0 || h.rotation.saves != 0 {
				t.Fatalf("authentication handoff error=%v refreshes=%d saves=%d", err, len(h.rotation.refreshes), h.rotation.saves)
			}
			for _, request := range h.opens {
				if strings.Join(request.Headers["Authorization"], "") != "Bearer access-start" {
					t.Error("plugin changed credentials while handing authentication failure to the host")
				}
			}
		})
	}
}

func TestChatCorrectionsHaveFourBoundedAttempts(t *testing.T) {
	for _, tc := range []struct {
		codes []int
		want  []string
	}{
		{[]int{11128, 11128, 11153}, []string{recoveryOriginal, recoveryComma, recoverySystem, recoverySystemAfter}},
		{[]int{11128, 11153, 11128}, []string{recoveryOriginal, recoveryComma, recoveryCommaAfter, recoverySystemAfter}},
		{[]int{11153, 11128, 11128}, []string{recoveryOriginal, recoveryAfter, recoveryCommaAfter, recoverySystemAfter}},
	} {
		for _, terminal := range []int{200, 11128, 11153} {
			t.Run(fmt.Sprint(tc.codes, terminal), func(t *testing.T) {
				s := workbuddy.New()
				defer s.Close()
				h := newChatRecoveryHost(t, append(tc.codes, terminal)...)
				err := h.execute(s, recoveryInput(parsedIdentity(t, "workbuddy", ""), "wb/common", recoveryOriginal), tc.want...)
				if (err == nil) != (terminal == 200) {
					t.Fatalf("bounded correction result=%v terminal=%d", err, terminal)
				}
			})
		}
	}
}

func TestChatRecoveryRemembersOnlyAcceptedCorrections(t *testing.T) {
	for _, tc := range []struct {
		name     string
		script   []int
		messages []string
		accepted string
	}{
		{"comma", []int{11128, 429}, []string{recoveryOriginal, recoveryComma}, recoveryComma},
		{"system", []int{11128, 11128, 503}, []string{recoveryOriginal, recoveryComma, recoverySystem}, recoveryRememberedSystem},
		{"aftertools", []int{11153, 429}, []string{recoveryOriginal, recoveryAfter}, recoveryAfter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy", "")
			req := recoveryInput(auth, "wb/common", recoveryOriginal)
			failed := newChatRecoveryHost(t, tc.script...)
			if err := failed.execute(s, req, tc.messages...); err == nil {
				t.Fatal("failed correction returned success")
			}
			pristine := newChatRecoveryHost(t, 200)
			if err := pristine.execute(s, req, recoveryOriginal); err != nil {
				t.Fatal(err)
			}
			acceptedScript := append([]int(nil), tc.script...)
			acceptedScript[len(acceptedScript)-1] = 200
			accepted := newChatRecoveryHost(t, acceptedScript...)
			accepted.onOpen = func(index int) {
				if index == len(acceptedScript)-1 {
					// Opening the corrected request has not accepted it yet.
					pending := newChatRecoveryHost(t, 200)
					if err := pending.execute(s, req, recoveryOriginal); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := accepted.execute(s, req, tc.messages...); err != nil {
				t.Fatal(err)
			}
			remembered := newChatRecoveryHost(t, 200)
			if err := remembered.execute(s, req, tc.accepted); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatRecoverySystemPolicyIsolation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		script     []int
		want       []string
		remembered string
	}{
		{"comma", []int{11128, 200}, []string{recoveryOriginal, recoveryComma}, recoveryComma},
		{"system", []int{11128, 11128, 200}, []string{recoveryOriginal, recoveryComma, recoverySystem}, recoveryRememberedSystem},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy", "")
			trained := newChatRecoveryHost(t, tc.script...)
			if err := trained.execute(s, recoveryInput(auth, "wb/common", recoveryOriginal), tc.want...); err != nil {
				t.Fatal(err)
			}
			for _, probe := range []struct {
				name, site, enterprise, model, messages string
			}{
				{"site", "workbuddy-ai", "", "wb-ai/common", recoveryOriginal},
				{"account", "workbuddy", "other-team", "wb/common", recoveryOriginal},
				{"model", "workbuddy", "", "wb/glm-5.3", recoveryOriginal},
				{"system-text", "workbuddy", "", "wb/common", `[{"role":"system","content":"You are an AI agent powered by Another Harness."},{"role":"user","content":"hello"}]`},
				{"system-block-shape", "workbuddy", "", "wb/common", `[{"role":"system","content":[{"type":"text","text":"You are an AI agent powered by Example Harness."}]},{"role":"user","content":"hello"}]`},
				{"system-metadata", "workbuddy", "", "wb/common", `[{"role":"system","name":"another-policy","content":"You are an AI agent powered by Example Harness."},{"role":"user","content":"hello"}]`},
			} {
				t.Run(probe.name, func(t *testing.T) {
					h := newChatRecoveryHost(t, 200)
					identity := parsedIdentity(t, probe.site, probe.enterprise)
					if err := h.execute(s, recoveryInput(identity, probe.model, probe.messages), probe.messages); err != nil {
						t.Fatal(err)
					}
				})
			}
			remembered := newChatRecoveryHost(t, 200)
			if err := remembered.execute(s, recoveryInput(auth, "wb/common", recoveryOriginal), tc.remembered); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatRecoveryAfterToolsPolicyIsScopedToAccountModelAndApplicableHistory(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	trained := newChatRecoveryHost(t, 11153, 200)
	if err := trained.execute(s, recoveryInput(auth, "wb/common", recoveryOriginal), recoveryOriginal, recoveryAfter); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct {
		site, enterprise, model string
	}{
		{"workbuddy-ai", "", "wb-ai/common"},
		{"workbuddy", "other-team", "wb/common"},
		{"workbuddy", "", "wb/glm-5.3"},
	} {
		h := newChatRecoveryHost(t, 200)
		if err := h.execute(s, recoveryInput(parsedIdentity(t, probe.site, probe.enterprise), probe.model, recoveryOriginal), recoveryOriginal); err != nil {
			t.Fatal(err)
		}
	}
	const changedSystem = `[{"role":"system","content":"Another policy."},{"role":"user","content":"Read the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"user","content":"Continue."}]`
	const changedSystemAfter = `[{"role":"system","content":"Another policy."},{"role":"user","content":"Read the file."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"assistant","content":""},{"role":"user","content":"Continue."}]`
	h := newChatRecoveryHost(t, 200)
	if err := h.execute(s, recoveryInput(auth, "wb/common", changedSystem), changedSystemAfter); err != nil {
		t.Fatal(err)
	}
	const plain = `[{"role":"system","content":"Another policy."},{"role":"user","content":"hello"}]`
	inapplicable := newChatRecoveryHost(t, 200)
	if err := inapplicable.execute(s, recoveryInput(auth, "wb/common", plain), plain); err != nil {
		t.Fatal(err)
	}
}

func TestChatRecoveryAssistantOnlyCommaDoesNotBecomeSystemPolicy(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	const original = `[{"role":"system","content":"Be brief."},{"role":"user","content":"hello"},{"role":"assistant","content":"You are an AI assistant running on Example."},{"role":"user","content":"next"}]`
	const corrected = `[{"role":"system","content":"Be brief."},{"role":"user","content":"hello"},{"role":"assistant","content":"You are an AI assistant, running on Example."},{"role":"user","content":"next"}]`
	trained := newChatRecoveryHost(t, 11128, 200)
	if err := trained.execute(s, recoveryInput(auth, "wb/common", original), original, corrected); err != nil {
		t.Fatal(err)
	}
	pristine := newChatRecoveryHost(t, 200)
	if err := pristine.execute(s, recoveryInput(auth, "wb/common", original), original); err != nil {
		t.Fatal(err)
	}
}

func TestChatRecoveryDoesNotReplayHostRetryStatusesOrUnchangedShapes(t *testing.T) {
	const plain = `[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"hello"}]`
	for _, tc := range []struct {
		name     string
		code     int
		messages string
	}{
		{"rate-limit", 429, recoveryOriginal},
		{"server-error", 500, recoveryOriginal},
		{"unavailable", 503, recoveryOriginal},
		{"comma-and-system-no-change", 11128, plain},
		{"aftertools-no-change", 11153, plain},
		{"longer-code-is-not-aftertools", 111530, recoveryOriginal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			h := newChatRecoveryHost(t, tc.code)
			err := h.execute(s, recoveryInput(parsedIdentity(t, "workbuddy", ""), "wb/common", tc.messages), tc.messages)
			if err == nil {
				t.Fatal("upstream refusal became success")
			}
		})
	}
}

func TestChatRecoveryRecognizesStructuredChannelCodes(t *testing.T) {
	for i, code := range []string{`11128`, `"11128"`} {
		t.Run(code, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			h := newChatRecoveryHost(t, 11128, 11128, 200)
			field := "internal_code"
			if i == 1 {
				field = "code"
			}
			h.errorBody = func(int) []byte {
				return []byte(`{"error":{"` + field + `":` + code + `,"message":"请求被拦截，请重新发送"}}`)
			}
			req := recoveryInput(parsedIdentity(t, "workbuddy", ""), "wb/common", recoveryOriginal)
			if err := h.execute(s, req, recoveryOriginal, recoveryComma, recoverySystem); err != nil {
				t.Fatal(err)
			}
		})
	}
}
