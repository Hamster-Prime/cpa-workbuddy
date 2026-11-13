package workbuddy_test

import (
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

const messagesResponseStart = `{"type":"message_start","message":{"id":"msg-native","type":"message","role":"assistant","model":"claude-sonnet-5.5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":7,"output_tokens":0,"cache_creation_input_tokens":3,"cache_read_input_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":3},"reasoning_tokens":1},"provider":{"region":"cn"}}}`
const messagesResponseFinish = `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":11,"reasoning_tokens":5}}`
const messagesResponseStop = `{"type":"message_stop"}`
const messagesResponseTextStart = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
const messagesResponseTextDelta = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"世界🌍"}}`
const messagesResponseTextStop = `{"type":"content_block_stop","index":0}`

func messagesResponseFrame(kind, payload string) string {
	return "event: " + kind + "\ndata: " + payload + "\n\n"
}

func messagesResponseRequest(t *testing.T) map[string]any {
	t.Helper()
	auth := parsedIdentity(t, "workbuddy", "")
	return map[string]any{
		"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON,
		"Model": "wb/claude-sonnet-5.5", "Format": "claude", "SourceFormat": "claude",
		"Payload":          []byte(`{"model":"wb/claude-sonnet-5.5","max_tokens":2048,"messages":[{"role":"user","content":"hello"}]}`),
		"host_callback_id": "response-callback", "stream_id": "response-downstream",
	}
}

func messagesResponseExecute(t *testing.T, chunks [][]byte, streaming bool) ([]byte, *responseStreamHost, error) {
	t.Helper()
	s := workbuddy.New()
	t.Cleanup(s.Close)
	h := &responseStreamHost{chunks: chunks, closed: make(chan struct{})}
	request := messagesResponseRequest(t)
	request["Stream"] = streaming
	method := "executor.execute"
	if streaming {
		method = "executor.execute_stream"
	}
	result, err := invoke(s, context.Background(), method, request, h.call)
	if err != nil {
		return nil, h, err
	}
	if streaming {
		select {
		case <-h.closed:
		case <-time.After(15 * time.Second):
			t.Fatal("native stream did not close")
		}
		return nil, h, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Payload []byte }
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	return response.Payload, h, nil
}

func messagesResponseAssertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("response = %s; want %s", got, want)
	}
}

func TestNativeMessagesResponseAggregatesBlocksAndIndependentUsage(t *testing.T) {
	frames := []string{
		messagesResponseFrame("message_start", messagesResponseStart),
		messagesResponseFrame("ping", `{"type":"ping"}`),
		messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"tool-native","name":"Read_File","input":{},"provider":{"signed":true}}}`),
		messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"世"}}`),
		messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"考"}}`),
		messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"え🌍"}}`),
		messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-"}}`),
		messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"native"}}`),
		messagesResponseFrame("content_block_stop", `{"type":"content_block_stop","index":0}`),
		messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"界\",\"args\":[true,false,null,-1.25e+2,{\"escape\":\"\\u4e16\\n\"}]}"}}`),
		messagesResponseFrame("content_block_stop", `{"type":"content_block_stop","index":3}`),
		messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque-cipher","provider":{"v":2}}}`),
		messagesResponseFrame("content_block_stop", `{"type":"content_block_stop","index":1}`),
		messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":"答案","citations":[{"type":"char_location","document_index":0}]}}`),
		messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"!"}}`),
		messagesResponseFrame("content_block_stop", `{"type":"content_block_stop","index":2}`),
		messagesResponseFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":"END","provider_stop":{"v":1}},"usage":{"output_tokens":11,"reasoning_tokens":5,"output_tokens_details":{"reasoning_tokens":5},"service_tier":"standard"}}`),
		messagesResponseFrame("message_stop", messagesResponseStop),
	}
	for _, width := range []int{1, 7, 8192} {
		t.Run(fmt.Sprintf("chunk=%d", width), func(t *testing.T) {
			payload, h, err := messagesResponseExecute(t, responseChunks(strings.Join(frames, ""), width), false)
			if err != nil {
				t.Fatal(err)
			}
			messagesResponseAssertJSON(t, payload, `{"id":"msg-native","type":"message","role":"assistant","model":"wb/claude-sonnet-5.5","provider":{"region":"cn"},"content":[{"type":"thinking","thinking":"考え🌍","signature":"sig-native"},{"type":"redacted_thinking","data":"opaque-cipher","provider":{"v":2}},{"type":"text","text":"答案!","citations":[{"type":"char_location","document_index":0}]},{"type":"tool_use","id":"tool-native","name":"Read_File","input":{"q":"世界","args":[true,false,null,-125,{"escape":"世\n"}]},"provider":{"signed":true}}],"stop_reason":"tool_use","stop_sequence":"END","provider_stop":{"v":1},"usage":{"input_tokens":7,"output_tokens":11,"cache_creation_input_tokens":3,"cache_read_input_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":3},"reasoning_tokens":5,"output_tokens_details":{"reasoning_tokens":5},"service_tier":"standard"}}`)
			if h.upstreamClosed != 1 || h.callbackError != nil {
				t.Fatalf("native lease closes=%d callback=%v", h.upstreamClosed, h.callbackError)
			}
		})
	}
}

func TestNativeMessagesResponsePreservesEventOrderAcrossSSEBoundaries(t *testing.T) {
	text := ": keepalive\r\n" +
		"event: message_start\r\nid: native-1\r\nretry: 123\r\ndata: " + strings.Replace(messagesResponseStart, `,"message":`, ",\r\ndata: \"message\":", 1) + "\r\n\r\n" +
		messagesResponseFrame("ping", `{"type":"ping"}`) +
		messagesResponseFrame("content_block_start", messagesResponseTextStart) +
		messagesResponseFrame("content_block_delta", messagesResponseTextDelta) +
		messagesResponseFrame("content_block_stop", messagesResponseTextStop) +
		messagesResponseFrame("message_delta", messagesResponseFinish) +
		messagesResponseFrame("message_stop", messagesResponseStop)
	for _, width := range []int{1, 11, 8192} {
		t.Run(fmt.Sprintf("chunk=%d", width), func(t *testing.T) {
			_, h, err := messagesResponseExecute(t, responseChunks(text, width), true)
			if err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.streamError != "" || h.upstreamClosed != 1 || h.callbackError != nil {
				t.Fatalf("stream error=%q upstream closes=%d callback=%v", h.streamError, h.upstreamClosed, h.callbackError)
			}
			var kinds []string
			for _, frame := range h.emitted {
				if len(frame) == 0 {
					continue
				}
				if strings.Contains(string(frame), "[DONE]") {
					t.Fatal("native stream emitted OpenAI DONE")
				}
				parts := strings.SplitN(string(frame), "data: ", 2)
				if len(parts) != 2 {
					t.Fatalf("invalid native SSE: %q", frame)
				}
				var event struct {
					Type    string                 `json:"type"`
					Message struct{ Model string } `json:"message"`
				}
				if err := json.Unmarshal([]byte(strings.TrimSpace(parts[1])), &event); err != nil {
					t.Fatal(err)
				}
				kinds = append(kinds, event.Type)
				if event.Type == "message_start" && (event.Message.Model != "wb/claude-sonnet-5.5" || !strings.Contains(parts[0], "id: native-1\nretry: 123\n")) {
					t.Fatalf("native start lost model or SSE metadata: %s", frame)
				}
			}
			if !reflect.DeepEqual(kinds, []string{"message_start", "ping", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}) {
				t.Fatalf("native event order = %v", kinds)
			}
		})
	}
}

func TestNativeMessagesResponseRejectsMalformedAndIncompleteStates(t *testing.T) {
	start := messagesResponseFrame("message_start", messagesResponseStart)
	textStart := messagesResponseFrame("content_block_start", messagesResponseTextStart)
	textStop := messagesResponseFrame("content_block_stop", messagesResponseTextStop)
	finish := messagesResponseFrame("message_delta", messagesResponseFinish)
	stop := messagesResponseFrame("message_stop", messagesResponseStop)
	cases := []struct{ name, text, code string }{
		{"empty", "", "upstream_stream_incomplete"},
		{"done-is-not-native", responseFrame("[DONE]"), "upstream_stream_malformed"},
		{"stop-without-finish", start + stop, "upstream_stream_incomplete"},
		{"missing-message-stop", start + finish, "upstream_stream_incomplete"},
		{"unterminated-stop", start + finish + "event: message_stop\ndata: " + messagesResponseStop, "upstream_stream_incomplete"},
		{"duplicate-message-start", start + start, "upstream_stream_malformed"},
		{"content-before-start", textStart, "upstream_stream_malformed"},
		{"duplicate-block", start + textStart + textStart, "upstream_stream_malformed"},
		{"unknown-block-stop", start + textStop, "upstream_stream_malformed"},
		{"duplicate-block-stop", start + textStart + textStop + textStop, "upstream_stream_malformed"},
		{"delta-after-block-stop", start + textStart + textStop + messagesResponseFrame("content_block_delta", messagesResponseTextDelta), "upstream_stream_malformed"},
		{"finish-with-open-block", start + textStart + finish, "upstream_stream_malformed"},
		{"duplicate-message-delta", start + finish + finish, "upstream_stream_malformed"},
		{"block-after-finish", start + finish + textStart, "upstream_stream_malformed"},
		{"negative-index", start + messagesResponseFrame("content_block_start", strings.Replace(messagesResponseTextStart, `"index":0`, `"index":-1`, 1)), "upstream_stream_malformed"},
		{"null-index", start + messagesResponseFrame("content_block_start", strings.Replace(messagesResponseTextStart, `"index":0`, `"index":null`, 1)), "upstream_stream_malformed"},
		{"wrong-delta-type", start + textStart + messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"secret"}}`), "upstream_stream_malformed"},
		{"event-type-mismatch", messagesResponseFrame("ping", messagesResponseStart), "upstream_stream_malformed"},
		{"malformed-json", responseFrame(`{"type":"message_start","secret":"private-stream-secret"`), "upstream_stream_malformed"},
		{"invalid-utf8", responseFrame("{\"type\":\"ping\",\"x\":\"\xff\"}"), "upstream_stream_malformed"},
		{"native-error", messagesResponseFrame("error", `{"type":"error","error":{"type":"overloaded_error","message":"private-stream-secret"}}`), "upstream_stream_error"},
		{"version-error", messagesResponseFrame("error", `{"error":{"code":"10085","message":"private-stream-secret"}}`), "upstream_client_version_rejected"},
		{"negative-usage", start + messagesResponseFrame("message_delta", strings.Replace(messagesResponseFinish, `"output_tokens":11`, `"output_tokens":-1`, 1)), "upstream_stream_malformed"},
		{"null-usage", start + messagesResponseFrame("message_delta", strings.Replace(messagesResponseFinish, `"output_tokens":11`, `"output_tokens":null`, 1)), "upstream_stream_malformed"},
		{"unknown-event", start + messagesResponseFrame("future", `{"type":"future","value":"meaningful"}`), "unsupported_upstream_response"},
		{"unknown-delta", start + textStart + messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"future_delta","data":"meaningful"}}`), "unsupported_upstream_response"},
	}
	for _, tc := range cases {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, streaming), func(t *testing.T) {
				_, h, err := messagesResponseExecute(t, responseChunks(tc.text, 17), streaming)
				if streaming && err == nil {
					h.mu.Lock()
					defer h.mu.Unlock()
					if h.streamError == "" || strings.Contains(h.streamError, "private-stream-secret") || h.upstreamClosed != 1 {
						t.Fatalf("native stream failure=%q closes=%d", h.streamError, h.upstreamClosed)
					}
					for _, frame := range h.emitted {
						if strings.Contains(string(frame), `"type":"message_stop"`) || strings.Contains(string(frame), "[DONE]") {
							t.Fatalf("failed stream emitted terminal success: %s", frame)
						}
					}
					return
				}
				var detail *workbuddy.Error
				if !errors.As(err, &detail) || detail.Code != tc.code || strings.Contains(detail.Message, "private-stream-secret") {
					t.Fatalf("native error = %v; want safe %s", err, tc.code)
				}
				if h.upstreamClosed != 1 {
					t.Fatalf("native upstream closed %d times", h.upstreamClosed)
				}
			})
		}
	}
}

func TestNativeMessagesResponseValidatesFragmentedToolJSON(t *testing.T) {
	for _, input := range []string{`{`, `{"q":}`, `{"q":01}`, `{"q":1.}`, `{"q":1e+}`, `{"q":true false}`, `{"q":[1,]}`, `{"q":"\x"}`, `{} {}`, `[]`, `null`} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", input, streaming), func(t *testing.T) {
				text := messagesResponseFrame("message_start", messagesResponseStart) + messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool-1","name":"read","input":{}}}`)
				for part := range strings.SplitSeq(input, "") {
					raw, err := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": part}})
					if err != nil {
						t.Fatal(err)
					}
					text += messagesResponseFrame("content_block_delta", string(raw))
				}
				text += messagesResponseFrame("content_block_stop", messagesResponseTextStop) + messagesResponseFrame("message_delta", messagesResponseFinish) + messagesResponseFrame("message_stop", messagesResponseStop)
				_, h, err := messagesResponseExecute(t, responseChunks(text, 13), streaming)
				if streaming && err == nil {
					if h.streamError == "" || h.upstreamClosed != 1 {
						t.Fatalf("invalid JSON tool stream succeeded: error=%q closes=%d", h.streamError, h.upstreamClosed)
					}
					return
				}
				var detail *workbuddy.Error
				if !errors.As(err, &detail) || detail.Code != "upstream_stream_malformed" {
					t.Fatalf("invalid tool JSON returned %v", err)
				}
			})
		}
	}
}

func TestNativeMessagesResponseBoundsEventsAndAggregate(t *testing.T) {
	for _, text := range []string{
		"data: " + strings.Repeat("x", (1<<20)+1),
		"data: " + strings.Repeat("x", 600000) + "\ndata: " + strings.Repeat("y", 600000) + "\n\n",
	} {
		_, _, err := messagesResponseExecute(t, responseChunks(text, 4096), false)
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "upstream_stream_too_large" {
			t.Fatalf("oversized native SSE error = %v", err)
		}
	}
	chunks := [][]byte{[]byte(messagesResponseFrame("message_start", messagesResponseStart) + messagesResponseFrame("content_block_start", messagesResponseTextStart))}
	fragment := []byte(messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"`+strings.Repeat("x", 512<<10)+`"}}`))
	for range 66 {
		chunks = append(chunks, fragment)
	}
	_, _, err := messagesResponseExecute(t, chunks, false)
	var detail *workbuddy.Error
	if !errors.As(err, &detail) || detail.Code != "upstream_stream_too_large" {
		t.Fatalf("oversized native aggregate error = %v", err)
	}
}

func TestNativeMessagesResponseCancellationClosesLease(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", streaming), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := &responseStreamHost{chunks: responseChunks(messagesResponseFrame("message_start", messagesResponseStart), 9), closed: make(chan struct{})}
			request := messagesResponseRequest(t)
			request["Stream"] = streaming
			method := "executor.execute"
			if streaming {
				method = "executor.execute_stream"
			}
			_, err := invoke(s, ctx, method, request, func(method string, raw []byte) ([]byte, error) {
				if method == "host.http.stream_read" {
					cancel()
				}
				return h.call(method, raw)
			})
			if streaming && err == nil {
				select {
				case <-h.closed:
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled native stream did not close")
				}
				h.mu.Lock()
				defer h.mu.Unlock()
				if h.streamError == "" || h.upstreamClosed != 1 {
					t.Fatalf("cancelled native stream error=%q closes=%d", h.streamError, h.upstreamClosed)
				}
				return
			}
			var detail *workbuddy.Error
			if !errors.As(err, &detail) || detail.Code != "request_cancelled" || h.upstreamClosed != 1 {
				t.Fatalf("native cancellation=%v closes=%d", err, h.upstreamClosed)
			}
		})
	}
}

func TestNativeMessagesResponseStreamsToolAndThinkingFragmentsVerbatim(t *testing.T) {
	payloads := []struct{ kind, raw string }{
		{"message_start", messagesResponseStart},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"考"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"え"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-native"}}`},
		{"content_block_stop", messagesResponseTextStop},
		{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"cipher"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":1}`},
		{"content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool-native","name":"Read_File","input":{}}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"世"}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"界\"}"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":2}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":11,"reasoning_tokens":5}}`},
		{"message_stop", messagesResponseStop},
	}
	var text strings.Builder
	for _, event := range payloads {
		text.WriteString(messagesResponseFrame(event.kind, event.raw))
	}
	_, h, err := messagesResponseExecute(t, responseChunks(text.String(), 1), true)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.streamError != "" || h.upstreamClosed != 1 {
		t.Fatalf("native tool stream error=%q closes=%d", h.streamError, h.upstreamClosed)
	}
	index := 0
	for _, frame := range h.emitted {
		if len(frame) == 0 {
			continue
		}
		if index >= len(payloads) {
			t.Fatalf("extra native event: %s", frame)
		}
		prefix := "event: " + payloads[index].kind + "\ndata: "
		if !strings.HasPrefix(string(frame), prefix) {
			t.Fatalf("native frame %d = %s", index, frame)
		}
		want := payloads[index].raw
		if index == 0 {
			want = strings.Replace(want, `"model":"claude-sonnet-5.5"`, `"model":"wb/claude-sonnet-5.5"`, 1)
		}
		messagesResponseAssertJSON(t, []byte(strings.TrimSpace(strings.TrimPrefix(string(frame), prefix))), want)
		index++
	}
	if index != len(payloads) {
		t.Fatalf("native stream emitted %d events; want %d", index, len(payloads))
	}
}

func TestNativeMessagesResponseRejectsPostTerminalPayload(t *testing.T) {
	complete := messagesResponseFrame("message_start", messagesResponseStart) + messagesResponseFrame("message_delta", messagesResponseFinish) + messagesResponseFrame("message_stop", messagesResponseStop)
	for _, extra := range []string{messagesResponseFrame("message_stop", messagesResponseStop), messagesResponseFrame("ping", `{"type":"ping"}`), "data: {"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q/stream=%v", extra, streaming), func(t *testing.T) {
				_, h, err := messagesResponseExecute(t, [][]byte{[]byte(complete + extra)}, streaming)
				if streaming && err == nil {
					if h.streamError == "" {
						t.Fatal("post-terminal native payload succeeded")
					}
					for _, frame := range h.emitted {
						if strings.Contains(string(frame), `"type":"message_stop"`) {
							t.Fatal("post-terminal failure emitted a success terminal")
						}
					}
					return
				}
				var detail *workbuddy.Error
				if !errors.As(err, &detail) || detail.Code != "upstream_stream_malformed" {
					t.Fatalf("post-terminal error = %v", err)
				}
			})
		}
	}
}

func TestNativeMessagesResponsePreservesOpaqueBlocksAndRejectsUnrepresentableFields(t *testing.T) {
	text := messagesResponseFrame("message_start", messagesResponseStart) +
		messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":4,"content_block":{"type":"provider_result","payload":{"values":[true,1,"世"]}}}`) +
		messagesResponseFrame("content_block_stop", `{"type":"content_block_stop","index":4}`) +
		messagesResponseFrame("message_delta", messagesResponseFinish) + messagesResponseFrame("message_stop", messagesResponseStop)
	payload, _, err := messagesResponseExecute(t, responseChunks(text, 19), false)
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Content []json.RawMessage }
	if err := json.Unmarshal(payload, &response); err != nil || len(response.Content) != 1 {
		t.Fatalf("opaque response = %s; error=%v", payload, err)
	}
	messagesResponseAssertJSON(t, response.Content[0], `{"type":"provider_result","payload":{"values":[true,1,"世"]}}`)
	unknown := strings.Replace(text, messagesResponseFinish, strings.TrimSuffix(messagesResponseFinish, "}")+`,"unknown":{"meaningful":true}}`, 1)
	_, _, err = messagesResponseExecute(t, responseChunks(unknown, 13), false)
	var detail *workbuddy.Error
	if !errors.As(err, &detail) || detail.Code != "unsupported_upstream_response" {
		t.Fatalf("unrepresentable event fields returned %v", err)
	}
	_, h, err := messagesResponseExecute(t, responseChunks(unknown, 13), true)
	if err != nil || h.streamError != "" {
		t.Fatalf("preservable stream fields failed: %v %q", err, h.streamError)
	}
	found := false
	for _, frame := range h.emitted {
		found = found || strings.Contains(string(frame), `"unknown":{"meaningful":true}`)
	}
	if !found {
		t.Fatal("native streaming discarded unknown event metadata")
	}
}

func TestNativeMessagesResponseBoundsStreamingMetadata(t *testing.T) {
	var text strings.Builder
	text.WriteString(messagesResponseFrame("message_start", messagesResponseStart))
	for index := range 65537 {
		fmt.Fprintf(&text, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n", index)
	}
	_, h, err := messagesResponseExecute(t, responseChunks(text.String(), 65536), true)
	if err != nil {
		t.Fatal(err)
	}
	if h.streamError == "" || !strings.Contains(h.streamError, "response limit") || h.upstreamClosed != 1 {
		t.Fatalf("unbounded native metadata: error=%q closes=%d", h.streamError, h.upstreamClosed)
	}
}

func TestNativeMessagesResponseRejectsBrokenThinkingAndToolStates(t *testing.T) {
	start := messagesResponseFrame("message_start", messagesResponseStart)
	tool := messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"same-id","name":"read","input":{}}}`)
	thinking := messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"private-thinking"}}`)
	cases := []string{
		start + tool + messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"same-id","name":"write","input":{}}}`),
		start + thinking + messagesResponseFrame("content_block_stop", messagesResponseTextStop),
		start + thinking + messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed"}}`) + messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"late"}}`),
		start + messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool","name":"read","input":{"old":true}}}`) + messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`),
	}
	for index, text := range cases {
		_, _, err := messagesResponseExecute(t, responseChunks(text, 5), false)
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "upstream_stream_malformed" || strings.Contains(detail.Message, "private-thinking") {
			t.Fatalf("broken native block %d returned %v", index, err)
		}
	}
}

func TestNativeMessagesResponsePreservesEmptyAndCompleteToolInputs(t *testing.T) {
	for _, input := range []string{`{}`, `{"already":[1,true,"世界"]}`} {
		text := messagesResponseFrame("message_start", messagesResponseStart) +
			messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool","name":"read","input":`+input+`}}`) +
			messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`) +
			messagesResponseFrame("content_block_stop", messagesResponseTextStop) +
			messagesResponseFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{}}`) + messagesResponseFrame("message_stop", messagesResponseStop)
		payload, _, err := messagesResponseExecute(t, responseChunks(text, 7), false)
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Content []struct{ Input json.RawMessage }
			Usage   map[string]json.RawMessage
		}
		if err := json.Unmarshal(payload, &response); err != nil || len(response.Content) != 1 {
			t.Fatalf("native empty input response=%s error=%v", payload, err)
		}
		messagesResponseAssertJSON(t, response.Content[0].Input, input)
		if _, exists := response.Usage["total_tokens"]; exists {
			t.Fatal("native response invented usage totals")
		}
	}
}

func TestNativeMessagesResponseStreamsBeyondAggregateBodyLimit(t *testing.T) {
	chunks := [][]byte{[]byte(messagesResponseFrame("message_start", messagesResponseStart) + messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool-large","name":"read","input":{}}}`) + messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"body\":\""}}`))}
	fragment := []byte(messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"`+strings.Repeat("x", 512<<10)+`"}}`))
	for range 65 {
		chunks = append(chunks, fragment)
	}
	chunks = append(chunks, []byte(messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"}"}}`)+messagesResponseFrame("content_block_stop", messagesResponseTextStop)+messagesResponseFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":12}}`)+messagesResponseFrame("message_stop", messagesResponseStop)))
	_, h, err := messagesResponseExecute(t, chunks, true)
	if err != nil || h.streamError != "" || h.upstreamClosed != 1 {
		t.Fatalf("large native streaming failed: %v %q closes=%d", err, h.streamError, h.upstreamClosed)
	}
	size := 0
	for _, frame := range h.emitted {
		size += len(frame)
	}
	if size <= 32<<20 {
		t.Fatalf("large native stream emitted only %d bytes", size)
	}
}

func TestNativeMessagesResponseMergesUsageBeforeFinishWithoutInventingCounters(t *testing.T) {
	start := messagesResponseFrame("message_start", strings.Replace(messagesResponseStart, `"usage":{"input_tokens":7,"output_tokens":0,"cache_creation_input_tokens":3,"cache_read_input_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":3},"reasoning_tokens":1}`, `"usage":{"input_tokens":7,"cache_read_input_tokens":2}`, 1))
	usage := messagesResponseFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":null,"stop_sequence":null},"usage":{"output_tokens":3,"reasoning_tokens":1}}`)
	finish := messagesResponseFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"stop_sequence","stop_sequence":"HALT"},"usage":{"output_tokens":5}}`)
	text := start + usage + finish + messagesResponseFrame("message_stop", messagesResponseStop)
	payload, _, err := messagesResponseExecute(t, responseChunks(text, 1), false)
	if err != nil {
		t.Fatal(err)
	}
	messagesResponseAssertJSON(t, payload, `{"id":"msg-native","type":"message","role":"assistant","model":"wb/claude-sonnet-5.5","content":[],"stop_reason":"stop_sequence","stop_sequence":"HALT","provider":{"region":"cn"},"usage":{"input_tokens":7,"cache_read_input_tokens":2,"output_tokens":5,"reasoning_tokens":1}}`)
	_, h, err := messagesResponseExecute(t, responseChunks(text, 1), true)
	if err != nil || h.streamError != "" {
		t.Fatalf("incremental native message deltas failed: %v %q", err, h.streamError)
	}
	_, _, err = messagesResponseExecute(t, responseChunks(start+usage+messagesResponseFrame("message_stop", messagesResponseStop), 7), false)
	var detail *workbuddy.Error
	if !errors.As(err, &detail) || detail.Code != "upstream_stream_incomplete" {
		t.Fatalf("usage-only deltas fabricated completion: %v", err)
	}
	_, _, err = messagesResponseExecute(t, responseChunks(start+usage+messagesResponseFrame("content_block_start", messagesResponseTextStart), 7), false)
	if !errors.As(err, &detail) || detail.Code != "upstream_stream_malformed" {
		t.Fatalf("content after message delta returned %v", err)
	}
}
