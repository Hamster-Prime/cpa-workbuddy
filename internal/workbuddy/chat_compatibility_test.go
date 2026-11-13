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

// compatibilityRequests exercises correction through the public executor and
// real host stream envelopes. reject chooses an upstream application code.
func compatibilityRequests(t *testing.T, s *workbuddy.Service, req map[string]any, reject func(map[string]json.RawMessage) int) []httpRequest {
	t.Helper()
	var sent []httpRequest
	var rejection []byte
	read := false
	_, err := invoke(s, context.Background(), "executor.execute", req, func(method string, raw []byte) ([]byte, error) {
		switch method {
		case "host.http.do_stream":
			var request httpRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			sent = append(sent, request)
			read = false
			rejection = nil
			code := reject(requestProtocolBody(t, request))
			status := 200
			if code != 0 {
				status = 400
				message := "Illegal API invocation from an unapproved channel"
				if code == 11153 {
					message = "the conversation must end with a user message"
				}
				var err error
				rejection, err = json.Marshal(map[string]any{"code": code, "msg": message})
				if err != nil {
					return nil, err
				}
			}
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": status, "stream_id": "compatibility-upstream"}})
		case "host.http.stream_read":
			if read {
				return []byte(`{"ok":true,"result":{"done":true}}`), nil
			}
			read = true
			payload := rejection
			if payload == nil {
				payload = []byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": payload, "done": true}})
		case "host.http.stream_close", "host.log":
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			return nil, errors.New("unexpected compatibility callback: " + method)
		}
	})
	if err != nil {
		t.Fatalf("compatibility execution: %v", err)
	}
	return sent
}

func compatibilityJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func compatibilityWireMessages(t *testing.T, body map[string]json.RawMessage) []map[string]json.RawMessage {
	t.Helper()
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(body["messages"], &messages); err != nil {
		t.Fatal(err)
	}
	return messages
}

func TestChatCompatibilityCommaScopesRolesAndPreservesBlocks(t *testing.T) {
	for _, tc := range []struct {
		name, system, assistant, wantSystem, wantAssistant string
	}{
		{"system-string", `"You are an AI agent powered by Example Harness."`, `"You are the deployment assistant built on Bar."`, `"You are an AI agent, powered by Example Harness."`, `"You are the deployment assistant, built on Bar."`},
		{"assistant-blocks", `[{"type":"text","text":"Be brief.","cache_control":{"type":"ephemeral"}}]`, `[{"type":"text","text":"You are an AI assistant running on Foo.","extension":{"seed":9007199254740993}}]`, `[{"type":"text","text":"Be brief.","cache_control":{"type":"ephemeral"}}]`, `[{"type":"text","text":"You are an AI assistant, running on Foo.","extension":{"seed":9007199254740993}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy", "")
			payload := []byte(`{"messages":[{"role":"system","content":` + tc.system + `},{"role":"user","content":[{"type":"text","text":"Quote: You are an assistant built on Rails.","extension":"kept"}]},{"role":"assistant","content":` + tc.assistant + `},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"DSH.md\"}"}}]},{"role":"tool","tool_call_id":"c1","content":"You are a coding agent powered by Example Harness."}]}`)
			sent := compatibilityRequests(t, s, requestProtocolInput(auth, "wb/common", payload), func(body map[string]json.RawMessage) int {
				messages := compatibilityWireMessages(t, body)
				if strings.Contains(string(messages[0]["content"]), "agent powered by") || strings.Contains(string(messages[2]["content"]), "assistant built on") || strings.Contains(string(messages[2]["content"]), "assistant running on") {
					return 11128
				}
				return 0
			})
			if len(sent) != 2 {
				t.Fatalf("comma requests = %d, want 2", len(sent))
			}
			messages := compatibilityWireMessages(t, requestProtocolBody(t, sent[1]))
			compatibilityJSON(t, messages[0]["content"], tc.wantSystem)
			compatibilityJSON(t, messages[2]["content"], tc.wantAssistant)
			compatibilityJSON(t, messages[1]["content"], `[{"type":"text","text":"Quote: You are an assistant built on Rails.","extension":"kept"}]`)
			compatibilityJSON(t, messages[3]["tool_calls"], `[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"DSH.md\"}"}}]`)
			compatibilityJSON(t, messages[4]["content"], `"You are a coding agent powered by Example Harness."`)
			if string(messages[0]["role"]) != `"system"` {
				t.Fatal("comma correction moved the system prompt")
			}
		})
	}
}

func TestChatCompatibilityMovesSimpleSystemAsUpstream(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	payload := []byte(`{"messages":[{"role":"system","content":"Example fingerprint"},{"role":"system","content":"Be brief."},{"role":"user","content":"Read calc.py.","name":"caller"},{"role":"assistant","content":"ok"},{"role":"system","content":"Reminder.","extension":{"keep":true}},{"role":"user","content":"next"}]}`)
	sent := compatibilityRequests(t, s, requestProtocolInput(auth, "wb/common", payload), func(body map[string]json.RawMessage) int {
		messages := compatibilityWireMessages(t, body)
		if string(messages[0]["content"]) != `"You are a helpful assistant."` {
			return 11128
		}
		return 0
	})
	if len(sent) != 2 {
		t.Fatalf("system move requests = %d, want 2", len(sent))
	}
	body := requestProtocolBody(t, sent[1])
	compatibilityJSON(t, body["messages"], `[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"<instructions>\nExample fingerprint\n\nBe brief.\n</instructions>\n\nRead calc.py.","name":"caller"},{"role":"assistant","content":"ok"},{"role":"user","content":"Reminder.","extension":{"keep":true}},{"role":"user","content":"next"}]`)
}

func TestChatCompatibilityMovesSystemBlocksWithoutLosingMetadata(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	payload := []byte(`{"messages":[{"role":"system","name":"agent-policy","extension":{"seed":9007199254740993},"content":[{"type":"text","text":"Example fingerprint","cache_control":{"type":"ephemeral"}},{"type":"text","text":"Be brief.","extension":"kept"}]},{"role":"user","content":[{"type":"text","text":"look","extension":"user-block"}]}]}`)
	sent := compatibilityRequests(t, s, requestProtocolInput(auth, "wb/common", payload), func(body map[string]json.RawMessage) int {
		messages := compatibilityWireMessages(t, body)
		if string(messages[0]["content"]) != `"You are a helpful assistant."` {
			return 11128
		}
		return 0
	})
	if len(sent) != 2 {
		t.Fatalf("system block requests = %d, want 2", len(sent))
	}
	messages := compatibilityWireMessages(t, requestProtocolBody(t, sent[1]))
	if len(messages) != 3 || string(messages[1]["role"]) != `"user"` || string(messages[1]["name"]) != `"agent-policy"` || string(messages[1]["extension"]) != `{"seed":9007199254740993}` {
		t.Fatalf("system metadata was lost: %s", requestProtocolBody(t, sent[1])["messages"])
	}
	compatibilityJSON(t, messages[0]["content"], `"You are a helpful assistant."`)
	compatibilityJSON(t, messages[1]["content"], `[{"type":"text","text":"<instructions>\n"},{"type":"text","text":"Example fingerprint","cache_control":{"type":"ephemeral"}},{"type":"text","text":"Be brief.","extension":"kept"},{"type":"text","text":"\n</instructions>"}]`)
	compatibilityJSON(t, messages[2]["content"], `[{"type":"text","text":"look","extension":"user-block"}]`)
}

func TestChatCompatibilityAfterToolsInsertsBeforeWholeUserSuffix(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy-ai", "")
	payload := []byte(`{"messages":[{"role":"user","content":"Read DSH.md"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"DSH output"},{"role":"user","content":"first reminder"},{"role":"user","content":[{"type":"text","text":"second reminder","extension":true}]}]}`)
	sent := compatibilityRequests(t, s, requestProtocolInput(auth, "wb-ai/common", payload), func(body map[string]json.RawMessage) int {
		messages := compatibilityWireMessages(t, body)
		if len(messages) == 6 {
			return 11153
		}
		return 0
	})
	if len(sent) != 2 {
		t.Fatalf("aftertools requests = %d, want 2", len(sent))
	}
	body := requestProtocolBody(t, sent[1])
	compatibilityJSON(t, body["messages"], `[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"Read DSH.md"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"DSH output"},{"role":"assistant","content":""},{"role":"user","content":"first reminder"},{"role":"user","content":[{"type":"text","text":"second reminder","extension":true}]}]`)
}

func TestChatHarnessAndFlaggedPreflightProtectsPathsAndToolResults(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	payload := []byte(`{"messages":[{"role":"system","content":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.280;"},{"type":"text","text":"You are an AI agent powered by DeepSeek Harness. Use DSH. Working directories /root/DSH/Work/OC-182.92.66.95/ C:\\Users\\me\\DSH\\x ~/DSH /srv/DeepSeek Harness/bin. Read DSH.md; DSH_HOME=~/.dsh; file:DSH; @DSH; DSH-2.","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":"Current DeepSeek Harness implementation checkout: /opt/dsh. Current DeepSeek Harness Web GUI URL: http://localhost/"},{"type":"text","text":"I like DSH and DeepSeek Harness. Run dsh --help in ~/.dsh; repeat /root/DSH/Work/OC-182.92.66.95/"},{"type":"text","text":"Main branch (you will usually use this for PRs): main"}]},{"role":"assistant","content":"DSH says hi. Codex CLI is an open source project led by OpenAI."},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"DSH.md\"}"}}]},{"role":"tool","tool_call_id":"c1","content":"README: DeepSeek Harness (DSH), DSH_HOME=~/.dsh"}],"tools":[{"type":"function","function":{"name":"read","description":"DeepSeek Harness uses DSH; read DSH.md","parameters":{"type":"object","properties":{"path":{"type":"string","description":"where DSH puts it","default":"/root/DSH/file"}}}}}]}`)
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/common", payload)))
	messages := compatibilityWireMessages(t, body)
	compatibilityJSON(t, messages[0]["content"], `[{"type":"text","text":"You are an AI agent. Use the agent harness. Working directories /root/DSH/Work/OC-182.92.66.95/ C:\\Users\\me\\DSH\\x ~/DSH /srv/DeepSeek Harness/bin. Read DSH.md; DSH_HOME=~/.dsh; file:DSH; @DSH; DSH-2.","cache_control":{"type":"ephemeral"}}]`)
	compatibilityJSON(t, messages[1]["content"], `[{"type":"text","text":"Current agent implementation checkout: /opt/dsh. Current Web GUI URL: http://localhost/"},{"type":"text","text":"I like DSH and DeepSeek Harness. Run dsh --help in ~/.dsh; repeat /root/DSH/Work/OC-182.92.66.95/"},{"type":"text","text":"Main branch (usually the base for PRs): main"}]`)
	compatibilityJSON(t, messages[2]["content"], `"DSH says hi. The Codex CLI is an open source project led by OpenAI."`)
	compatibilityJSON(t, messages[4]["content"], `"README: DeepSeek Harness (DSH), DSH_HOME=~/.dsh"`)
	compatibilityJSON(t, body["tools"], `[{"type":"function","function":{"name":"read","description":"the agent harness uses the agent harness; read DSH.md","parameters":{"type":"object","properties":{"path":{"type":"string","description":"where the agent harness puts it","default":"/root/DSH/file"}}}}}]`)
}

func TestChatHarnessBillingOnlySystemGetsDefault(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/common", []byte(`{"messages":[{"role":"system","content":"x-anthropic-billing-header: cc_version=2.1.280;"},{"role":"user","content":"You are Claude Code, Anthropic’s official CLI for Claude."}]}`))))
	compatibilityJSON(t, body["messages"], `[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"You are Claude Code, Anthropic's CLI for Claude."}]`)
}

func TestMessagesHarnessPreflightPreservesNativeFlagsAndToolBlocks(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth, _ := discoverChatCapabilities(t, "workbuddy", "harness-native", chatCapabilityConfig)
	payload := []byte(`{"max_tokens":512,"system":[{"type":"text","text":"You are an AI agent powered by DeepSeek Harness. Read DSH.md","cache_control":{"type":"ephemeral"}},{"type":"text","text":"x-anthropic-billing-header: native-kept"}],"messages":[{"role":"user","content":"You are Claude Code, Anthropic's official CLI for Claude."},{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"read","input":{"path":"DSH.md"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"You are an AI agent powered by DeepSeek Harness. DSH output"},{"type":"text","text":"Current DeepSeek Harness implementation checkout: /opt/dsh.","extension":"kept"}]}],"tools":[{"name":"read","description":"DSH reads DSH.md","input_schema":{"type":"object","properties":{"path":{"type":"string","description":"where DSH puts it"}}}}]}`)
	body := messagesRequestBody(t, executeMessagesRequest(t, s, messagesRequestInput(auth, payload)))
	compatibilityJSON(t, body["system"], `[{"type":"text","text":"You are an AI agent. Read DSH.md","cache_control":{"type":"ephemeral"}},{"type":"text","text":"x-anthropic-billing-header: native-kept"}]`)
	messages := compatibilityWireMessages(t, body)
	compatibilityJSON(t, messages[0]["content"], `"You are Claude Code, Anthropic's official CLI for Claude."`)
	compatibilityJSON(t, messages[1]["content"], `[{"type":"tool_use","id":"c1","name":"read","input":{"path":"DSH.md"}}]`)
	compatibilityJSON(t, messages[2]["content"], `[{"type":"tool_result","tool_use_id":"c1","content":"You are an AI agent powered by DeepSeek Harness. DSH output"},{"type":"text","text":"Current agent implementation checkout: /opt/dsh.","extension":"kept"}]`)
	compatibilityJSON(t, body["tools"], `[{"name":"read","description":"the agent harness reads DSH.md","input_schema":{"type":"object","properties":{"path":{"type":"string","description":"where the agent harness puts it"}}}}]`)
	if _, exists := body["extra_vars"]; exists {
		t.Fatal("native Messages acquired Chat growth telemetry")
	}
}

func compatibilityGrowthEvents(t *testing.T, body map[string]json.RawMessage) (map[string]json.RawMessage, []map[string]json.RawMessage) {
	t.Helper()
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(body["extra_vars"], &vars); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal(vars["growthEvent"], &text); err != nil {
		t.Fatal(err)
	}
	var events []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &events); err != nil {
		t.Fatal(err)
	}
	return vars, events
}

func TestChatGrowthPreservesCallerEventsDeduplicatesAndCountsUTF16(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy-ai", "growth")
	payload := []byte(`{"extra_vars":{"a":9007199254740993,"initiator":"agent","growthEvent":"[{\"eventCode\":\"expert_actual_use\",\"id\":\"e1\",\"extension\":{\"keep\":true}}]"},"messages":[{"role":"user","content":[{"type":"text","text":"run 😀"},{"text":"测试"}]},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"long tool output"},{"role":"system","content":"effort high"},{"role":"user","content":"a tool attachment, not the input"}]}`)
	req := requestProtocolInput(auth, "wb-ai/custom-model", payload)
	req["Headers"] = map[string][]string{"X-Conversation-ID": {"growth-fixed"}}
	first := executeRequestProtocol(t, s, req)
	vars, events := compatibilityGrowthEvents(t, requestProtocolBody(t, first))
	if len(events) != 2 || string(vars["initiator"]) != `"agent"` || string(vars["a"]) != "9007199254740993" {
		t.Fatalf("caller growth fields were lost: %v", vars)
	}
	compatibilityJSON(t, events[0]["eventCode"], `"expert_actual_use"`)
	compatibilityJSON(t, events[0]["extension"], `{"keep":true}`)
	compatibilityJSON(t, events[1]["extra"], `{"inputLength":8,"requestModelId":"custom-model","requestModelName":"custom-model","mode":"craft","command":""}`)
	var conversation string
	if err := json.Unmarshal(events[1]["id"], &conversation); err != nil {
		t.Fatal(err)
	}
	if conversation == "" || len(first.Headers["X-Conversation-ID"]) != 1 || conversation != first.Headers["X-Conversation-ID"][0] {
		t.Fatal("growth event does not belong to the request conversation")
	}
	var caller map[string]json.RawMessage
	if err := json.Unmarshal(payload, &caller); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	caller["extra_vars"] = encoded
	req["Payload"], err = json.Marshal(caller)
	if err != nil {
		t.Fatal(err)
	}
	second := executeRequestProtocol(t, s, req)
	_, again := compatibilityGrowthEvents(t, requestProtocolBody(t, second))
	if len(again) != 2 {
		t.Fatalf("growth duplicated caller event: %d", len(again))
	}
	compatibilityJSON(t, again[1]["extra"], `{"inputLength":8,"requestModelId":"custom-model","requestModelName":"custom-model","mode":"craft","command":""}`)
}

func TestChatGrowthCountsConsecutiveOwnUsersAndSkipsAssistantToolStep(t *testing.T) {
	for _, tc := range []struct {
		name, messages string
		length         int
	}{
		{"consecutive-users", `[{"role":"user","content":"first"},{"role":"user","content":"😀"}]`, 2},
		{"assistant-tool-step", `[{"role":"user","content":"own"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"user","content":"attachment"}]`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy-ai", "")
			body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb-ai/common", []byte(`{"messages":`+tc.messages+`}`))))
			vars, events := compatibilityGrowthEvents(t, body)
			if len(events) != 1 || string(vars["initiator"]) != `"user"` {
				t.Fatalf("default growth event = %v", events)
			}
			var extra struct {
				InputLength int `json:"inputLength"`
			}
			if err := json.Unmarshal(events[0]["extra"], &extra); err != nil {
				t.Fatal(err)
			}
			if extra.InputLength != tc.length {
				t.Fatalf("own input length = %d, want %d", extra.InputLength, tc.length)
			}
		})
	}
}

func TestChatHarnessNestingLimitRejectsBeforeTransport(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	tools := `{"type":"string","description":"DSH"}`
	for range 70 {
		tools = `{"type":"object","properties":{"nested":` + tools + `}}`
	}
	payload := []byte(`{"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"read","parameters":` + tools + `}}]}`)
	_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb/common", payload), func(string, []byte) ([]byte, error) {
		t.Error("overdeep compatibility tools reached the host")
		return nil, errors.New("unexpected callback")
	})
	var detail *workbuddy.Error
	if !errors.As(err, &detail) || detail.Code != "invalid_request" || detail.HTTPStatus != 400 {
		t.Fatalf("nesting error = %v", err)
	}
}

func TestChatCompatibilityAfterToolsIgnoresOtherShapes(t *testing.T) {
	for _, messages := range []string{
		`[{"role":"user","content":"hello"}]`,
		`[{"role":"user","content":"hello"},{"role":"assistant","content":"prefill"}]`,
		`[{"role":"user","content":"hello"},{"role":"tool","tool_call_id":"c1","content":"result"}]`,
		`[{"role":"user","content":"hello"},{"role":"tool","tool_call_id":"c1","content":"result"},{"role":"system","content":"intervening"},{"role":"user","content":"next"}]`,
	} {
		t.Run(messages, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy-ai", "")
			calls := 0
			_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb-ai/common", []byte(`{"messages":`+messages+`}`)), func(method string, raw []byte) ([]byte, error) {
				switch method {
				case "host.http.do_stream":
					calls++
					return []byte(`{"ok":true,"result":{"status_code":400,"stream_id":"aftertools-inapplicable"}}`), nil
				case "host.http.stream_read":
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": []byte(`{"code":11153,"msg":"the conversation must end with a user message"}`), "done": true}})
				case "host.http.stream_close", "host.log":
					return []byte(`{"ok":true,"result":{}}`), nil
				default:
					return nil, errors.New("unexpected compatibility callback")
				}
			})
			if err == nil || calls != 1 {
				t.Fatalf("inapplicable aftertools requests=%d error=%v", calls, err)
			}
		})
	}
}

func TestChatGrowthUsesVerifiedDisplayNameAndPreservesSingleCallerEvent(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth, _ := discoverChatCapabilities(t, "workbuddy-ai", "growth-names", `{"agents":[{"name":"cli","models":["common"]}],"models":[{"id":"common","name":"Verified Display"}]}`)
	payload := []byte(`{"extra_vars":{"growthEvent":"{\"eventCode\":\"expert_actual_use\",\"id\":\"single\"}"},"messages":[{"role":"user","content":"hi"}]}`)
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb-ai/common", payload)))
	vars, events := compatibilityGrowthEvents(t, body)
	if len(events) != 2 || string(vars["initiator"]) != `"user"` {
		t.Fatalf("single caller event or initiator lost: %v", vars)
	}
	compatibilityJSON(t, events[0]["eventCode"], `"expert_actual_use"`)
	compatibilityJSON(t, events[0]["id"], `"single"`)
	compatibilityJSON(t, events[1]["extra"], `{"inputLength":2,"requestModelId":"common","requestModelName":"Verified Display","mode":"craft","command":""}`)
}

func TestChatGrowthIsInternationalOnly(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/common", []byte(`{"messages":[{"role":"user","content":"hello"}]}`))))
	if _, exists := body["extra_vars"]; exists {
		t.Fatal("domestic Chat acquired international growth telemetry")
	}
}
