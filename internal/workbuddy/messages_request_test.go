package workbuddy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

const messagesRequestEvents = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-request\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-opus-5.5\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func messagesRequestInput(auth workbuddy.AuthData, payload []byte) map[string]any {
	req := requestProtocolInput(auth, "wb/claude-opus-5.5", payload)
	req["Format"], req["SourceFormat"] = "claude", "claude"
	return req
}

func executeMessagesRequest(t *testing.T, s *workbuddy.Service, req map[string]any) httpRequest {
	t.Helper()
	h := &responseStreamHost{chunks: responseChunks(messagesRequestEvents, 7), closed: make(chan struct{})}
	var sent httpRequest
	calls := 0
	_, err := invoke(s, context.Background(), "executor.execute", req, func(method string, raw []byte) ([]byte, error) {
		if method == "host.http.do_stream" {
			calls++
			if err := json.Unmarshal(raw, &sent); err != nil {
				return nil, err
			}
		}
		return h.call(method, raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || h.upstreamClosed != 1 {
		t.Fatalf("native requests=%d closes=%d", calls, h.upstreamClosed)
	}
	return sent
}

func messagesRequestBody(t *testing.T, request httpRequest) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if json.Unmarshal(request.Body, &body) != nil {
		t.Fatalf("Messages request is not native JSON: %s", request.Body)
	}
	return body
}

func TestMessagesRequestPreservesNativeShapeAndCredentialScope(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth, _ := discoverChatCapabilities(t, "workbuddy", "messages-enterprise", chatCapabilityConfig)
	payload := []byte(`{"model":"client-alias","max_tokens":64000,"stream":false,"system":[{"type":"text","text":"Native system","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"tools":[{"name":"fs.read","input_schema":{"anyOf":[{"type":"object","properties":{"path":{"type":"string"}}}]}}],"tool_choice":{"type":"tool","name":"fs.read"},"messages":[{"role":"user","content":"Read a file"},{"role":"assistant","content":[{"type":"thinking","thinking":"unsigned"},{"type":"thinking","thinking":"signed","signature":"CAQSvalid"},{"type":"tool_use","id":"toolu-read","name":"fs.read","input":{"path":"a.txt"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu-read","content":"contents"},{"type":"text","text":"Continue"}]}]}`)
	req := messagesRequestInput(auth, payload)
	req["Headers"] = map[string][]string{"Authorization": {"Bearer caller"}, "X-Api-Key": {"caller-key"}, "Content-Encoding": {"gzip"}, "Anthropic-Version": {"2023-06-01"}, "Anthropic-Beta": {"test-native-beta"}}
	sent := executeMessagesRequest(t, s, req)
	if sent.URL != "https://copilot.tencent.com/v1/messages" || sent.Method != "POST" || len(sent.Headers["Content-Encoding"]) != 0 || len(sent.Headers["X-Api-Key"]) != 0 {
		t.Fatal("native request did not use plain JSON at the fixed Messages endpoint")
	}
	if sent.Headers["Authorization"][0] != "Bearer fixture-browser-access" || sent.Headers["X-Enterprise-Id"][0] != "messages-enterprise" || sent.Headers["Anthropic-Version"][0] != "2023-06-01" || sent.Headers["Anthropic-Beta"][0] != "test-native-beta" {
		t.Fatal("Messages headers lost credential or client protocol scope")
	}
	for name, want := range map[string]string{"X-Agent-Type": "main", "X-Agent-Runtime": "local-agent", "X-Stainless-Retry-Count": "0", "X-Stainless-Timeout": "1200", "X-Stainless-Package-Version": "6.25.0"} {
		if got := sent.Headers[name]; len(got) != 1 || got[0] != want {
			t.Fatalf("native attribution %s=%v, want %s", name, got, want)
		}
	}
	body := messagesRequestBody(t, sent)
	var original map[string]json.RawMessage
	if err := json.Unmarshal(payload, &original); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"system", "tools", "tool_choice", "thinking", "output_config", "max_tokens"} {
		var got, want any
		if err := json.Unmarshal(body[key], &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(original[key], &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("native %s changed: %s", key, body[key])
		}
	}
	if string(body["model"]) != `"claude-opus-5.5"` || string(body["stream"]) != "true" || body["stream_options"] != nil {
		t.Fatal("native model/stream selection acquired Chat normalization")
	}
	if bytes.Contains(body["messages"], []byte("unsigned")) || !bytes.Contains(body["messages"], []byte(`"signature":"CAQSvalid"`)) || !bytes.Contains(body["messages"], []byte(`"name":"fs.read"`)) {
		t.Fatalf("thinking/tool history changed: %s", body["messages"])
	}
}

func TestMessagesRequestUnsignedHistoryAndToolTurnIdentity(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	other := parsedIdentity(t, "workbuddy", "other")
	first := `{"role":"user","content":[{"type":"text","text":"Read a file"}]}`
	tool := `,{"role":"assistant","content":[{"type":"thinking","thinking":"unsigned"},{"type":"tool_use","id":"t1","name":"Read","input":{}}]},{"role":"system","content":"environment"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"done"},{"type":"text","text":"metadata"}]}`
	ask := func(account workbuddy.AuthData, messages string) httpRequest {
		t.Helper()
		return executeMessagesRequest(t, s, messagesRequestInput(account, []byte(`{"max_tokens":64,"messages":[`+messages+`]}`)))
	}
	one := ask(auth, first)
	again := ask(auth, first)
	loop := ask(auth, first+tool)
	attached := ask(auth, first+tool+`,{"role":"user","content":"attached result"}`)
	next := ask(auth, first+tool+`,{"role":"assistant","content":[{"type":"text","text":"answer"}]},{"role":"user","content":"new question"}`)
	foreign := ask(other, first)
	for _, key := range []string{"X-Conversation-ID", "X-Conversation-Request-ID"} {
		if one.Headers[key][0] != again.Headers[key][0] || one.Headers[key][0] != loop.Headers[key][0] || one.Headers[key][0] != attached.Headers[key][0] || one.Headers[key][0] == foreign.Headers[key][0] {
			t.Fatalf("native %s drifted or crossed account", key)
		}
	}
	if one.Headers["X-Conversation-ID"][0] != next.Headers["X-Conversation-ID"][0] || one.Headers["X-Conversation-Request-ID"][0] == next.Headers["X-Conversation-Request-ID"][0] {
		t.Fatal("native next user did not change turn within conversation")
	}
	only := ask(auth, first+`,{"role":"assistant","content":[{"type":"thinking","thinking":"unsigned-only"}]},{"role":"user","content":"continue"}`)
	body := messagesRequestBody(t, only)
	if !bytes.Contains(body["messages"], []byte(`"content":[{"type":"text","text":""}]`)) || bytes.Contains(body["messages"], []byte("unsigned-only")) {
		t.Fatalf("unsigned-only block handling=%s", body["messages"])
	}
}

func TestMessagesRequestEffortAndNestedImageEligibility(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth, _ := discoverChatCapabilities(t, "workbuddy", "vision", chatCapabilityConfig)
	textOnly, _ := discoverChatCapabilities(t, "workbuddy", "text", `{"agents":[{"name":"cli","models":["claude-opus-5.5"]}],"models":[{"id":"claude-opus-5.5","reasoning":{"supportedEfforts":["high"]}}]}`)
	image := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}`
	for _, content := range []string{`[` + image + `]`, `[{"type":"tool_result","tool_use_id":"t1","content":[` + image + `]}]`} {
		payload := []byte(`{"max_tokens":64,"messages":[{"role":"user","content":` + content + `}]}`)
		executeMessagesRequest(t, s, messagesRequestInput(auth, payload))
		_, err := invoke(s, context.Background(), "executor.execute", messagesRequestInput(textOnly, payload), func(string, []byte) ([]byte, error) {
			t.Error("unadvertised nested image reached HTTP")
			return nil, errors.New("unexpected callback")
		})
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "unsupported_feature" {
			t.Fatalf("nested image error=%v", err)
		}
	}
	for _, extra := range []string{`,"output_config":{"effort":"max"}`, `,"thinking":{"type":"disabled"}`} {
		executeMessagesRequest(t, s, messagesRequestInput(auth, []byte(`{"max_tokens":64,"messages":[{"role":"user","content":"hi"}]`+extra+`}`)))
	}
	for _, extra := range []string{`,"output_config":{"effort":"low"}`, `,"output_config":{"effort":"none"}`, `,"reasoning_effort":"high"`} {
		_, err := invoke(s, context.Background(), "executor.execute", messagesRequestInput(auth, []byte(`{"max_tokens":64,"messages":[{"role":"user","content":"hi"}]`+extra+`}`)), func(string, []byte) ([]byte, error) {
			t.Error("unsupported native effort reached HTTP")
			return nil, errors.New("unexpected callback")
		})
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "unsupported_feature" {
			t.Fatalf("effort %s error=%v", extra, err)
		}
	}
}

func TestMessagesSignatureAndAuthenticationRecoveryAreJointlyBounded(t *testing.T) {
	for _, statuses := range [][]int{{400, 401, 200}, {401, 400, 200}, {400, 401, 400}} {
		t.Run(fmt.Sprint(statuses), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := refreshIdentity(t, "workbuddy", "", time.Hour)
			h := newRotationHost(t, auth)
			var opens []httpRequest
			var active int
			closes := 0
			call := func(method string, raw []byte) ([]byte, error) {
				switch method {
				case "host.http.do_stream":
					var request httpRequest
					if err := json.Unmarshal(raw, &request); err != nil {
						return nil, err
					}
					opens = append(opens, request)
					active = len(opens) - 1
					if active >= len(statuses) {
						return nil, errors.New("retry exceeded script")
					}
					headers := map[string][]string{}
					if statuses[active] == 401 {
						headers["WWW-Authenticate"] = []string{`Bearer error="invalid_token"`}
					}
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": statuses[active], "headers": headers, "stream_id": fmt.Sprintf("native-recovery-%d", active)}})
				case "host.http.stream_read":
					payload := []byte(messagesRequestEvents)
					if statuses[active] == 400 {
						payload = []byte(`{"error":{"code":"400001","internal_code":"11159"}}`)
					}
					if statuses[active] == 401 {
						payload = []byte(`{"error":{"code":"invalid_token"}}`)
					}
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": payload, "done": true}})
				case "host.http.stream_close":
					closes++
					return []byte(`{"ok":true,"result":{}}`), nil
				default:
					return h.call(method, raw)
				}
			}
			payload := []byte(`{"max_tokens":64,"messages":[{"role":"user","content":"first"},{"role":"assistant","content":[{"type":"thinking","thinking":"old","signature":"CAQSforeign"},{"type":"text","text":"answer"}]},{"role":"user","content":"continue"}]}`)
			_, err := invoke(s, context.Background(), "executor.execute", messagesRequestInput(auth, payload), call)
			if (statuses[2] == 200) != (err == nil) || len(opens) != 3 || len(h.refreshes) != 1 || closes != 3 {
				t.Fatalf("combined retries error=%v attempts=%d refresh=%d closes=%d", err, len(opens), len(h.refreshes), closes)
			}
			for _, request := range opens {
				if request.URL != "https://copilot.tencent.com/v1/messages" || request.Headers["X-Conversation-Request-ID"][0] != opens[0].Headers["X-Conversation-Request-ID"][0] {
					t.Fatal("combined retries changed route or user round")
				}
			}
			if strings.Contains(string(opens[2].Body), "CAQSforeign") || opens[2].Headers["Authorization"][0] != "Bearer access-next" {
				t.Fatal("combined recovery did not use corrected history and refreshed credential")
			}
		})
	}
}
