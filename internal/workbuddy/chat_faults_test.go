package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cpa-workbuddy/internal/workbuddy"
)

func TestExecutionFaultClassesAndSafeRetryHints(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		status                      int
		body, retry, code, upstream string
		wantStatus                  int
	}{
		{"restriction", 403, `{"code":11140,"msg":"request illegal","account":{"access_token":"private-access"}}`, "", "account_restricted", "11140", 403},
		{"credits", 400, `{"error":{"data":{"code":"14018"}}}`, "", "credit_package_exhausted", "14018", 400},
		{"version", 400, `{"code":10085,"msg":"private response"}`, "", "client_version_rejected", "10085", 502},
		{"context", 400, `{"error":{"code":"context_length_exceeded","message":"private request"}}`, "", "context_length_exceeded", "", 400},
		{"permission", 403, `{"code":11170,"msg":"model access denied"}`, "", "model_permission_denied", "11170", 403},
		{"rate", 429, `{"error":{"message":"private-access private-refresh private-state"}}`, "7", "rate_limit_exceeded", "", 429},
		{"server", 503, `{"error":{"message":"private upstream"}}`, "0", "upstream_unavailable", "", 503},
		{"timeout", 504, `{"msg":"private upstream"}`, "invalid", "upstream_timeout", "", 504},
		{"code-boundary", 400, `{"code":140185,"msg":"private upstream"}`, "", "upstream_http_failed", "140185", 400},
		{"invalid-header", 429, `{"code":429}`, "7\r\nAuthorization: private-access", "rate_limit_exceeded", "429", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			opens, closes, reads := 0, 0, 0
			_, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(parsedIdentity(t, "workbuddy", ""), false), func(method string, _ []byte) ([]byte, error) {
				switch method {
				case "host.http.do_stream":
					opens++
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": tc.status, "headers": map[string][]string{"Retry-After": {tc.retry}}, "stream_id": "fault"}})
				case "host.http.stream_read":
					reads++
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": []byte(tc.body), "done": true}})
				case "host.http.stream_close":
					closes++
					return []byte(`{"ok":true,"result":{}}`), nil
				default:
					return nil, errors.New("unexpected fault callback")
				}
			})
			detail, ok := errors.AsType[*workbuddy.Error](err)
			if !ok || detail.Code != tc.code || detail.HTTPStatus != tc.wantStatus || detail.UpstreamCode != tc.upstream {
				t.Fatalf("fault=%+v", detail)
			}
			wantRetry := tc.retry
			if tc.name == "timeout" || tc.name == "invalid-header" {
				wantRetry = ""
			}
			if detail.RetryAfter != wantRetry {
				t.Fatalf("retry=%q", detail.RetryAfter)
			}
			if strings.Contains(err.Error(), "private") || opens != 1 || closes != 1 || reads != 1 {
				t.Fatalf("error=%v calls=%d/%d/%d", err, opens, closes, reads)
			}
		})
	}
}

func TestExecutionErrorReadIsBoundedAndClosesUpstream(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	reads, closes := 0, 0
	_, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(parsedIdentity(t, "workbuddy", ""), false), func(method string, _ []byte) ([]byte, error) {
		switch method {
		case "host.http.do_stream":
			return []byte(`{"ok":true,"result":{"status_code":503,"stream_id":"large-error"}}`), nil
		case "host.http.stream_read":
			reads++
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": []byte(strings.Repeat("private", 8192)), "done": false}})
		case "host.http.stream_close":
			closes++
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			return nil, errors.New("unexpected bounded error callback")
		}
	})
	if err == nil || reads != 2 || closes != 1 || strings.Contains(err.Error(), "private") {
		t.Fatalf("err=%v reads=%d closes=%d", err, reads, closes)
	}
}

func TestLargeChatRequestUsesGzipAndPreservesContentFilter(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	text := strings.Repeat("你好🙂", 150000)
	req := chatWireRequest(parsedIdentity(t, "workbuddy-ai", ""), false)
	payload, err := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": text}}})
	if err != nil {
		t.Fatal(err)
	}
	req["Payload"] = payload
	opens := 0
	result, err := invoke(s, context.Background(), "executor.execute", req, func(method string, raw []byte) ([]byte, error) {
		switch method {
		case "host.http.do_stream":
			opens++
			var request httpRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			body := requestProtocolBody(t, request)
			messages := compatibilityWireMessages(t, body)
			var actual string
			if err := json.Unmarshal(messages[len(messages)-1]["content"], &actual); err != nil {
				return nil, err
			}
			if actual != text || request.Headers["Content-Encoding"][0] != "gzip" {
				t.Fatal("large request content or gzip lost")
			}
			return []byte(`{"ok":true,"result":{"status_code":200,"stream_id":"filtered"}}`), nil
		case "host.http.stream_read":
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": []byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Sorry, I can't respond to this question.\"},\"finish_reason\":\"content_filter\"}]}\n\ndata: [DONE]\n\n"), "done": true}})
		case "host.http.stream_close":
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			return nil, errors.New("unexpected large request callback")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.(workbuddy.ExecutorResponse)
	if !ok {
		t.Fatalf("unexpected execution result %T", result)
	}
	if opens != 1 || !strings.Contains(string(response.Payload), `"finish_reason":"content_filter"`) || !strings.Contains(string(response.Payload), "Sorry") {
		t.Fatalf("filter=%s opens=%d", response.Payload, opens)
	}
}
