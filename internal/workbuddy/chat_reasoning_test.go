package workbuddy_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"cpa-workbuddy/internal/workbuddy"
)

const reasoningFirst = `{"id":"reason-chat","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"思","reasoning":"独","reasoning_details":[{"index":3,"id":"thought-3","type":"reasoning.text","text":"考","signature":"sig-","provider":{"version":1,"tags":["opaque"]}},{"id":"summary-id","type":"reasoning.summary","summary":"要"}],"content":"答","tool_calls":[{"index":0,"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"q\":\""}}]},"finish_reason":null}]}`
const reasoningSecond = `{"choices":[{"index":0,"delta":{"reasoning_content":"考🌍","reasoning":"立","reasoning_details":[{"index":3,"text":"え🌍","signature":"tail","provider":{"tags":["opaque"],"version":1}},{"index":4,"id":"summary-id","summary":"約","format":"provider-v1"},{"index":7,"id":"encrypted","type":"reasoning.encrypted","data":"cipher-"}],"content":"案","tool_calls":[{"index":0,"function":{"arguments":"世界\"}"}}]},"finish_reason":null}]}`
const reasoningLast = `{"choices":[{"index":0,"delta":{"reasoning_details":[{"id":"encrypted","data":"text","custom":{"a":[1,true,null]}}]},"finish_reason":"tool_calls"}]}`
const reasoningUsage = `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":9,"total_tokens":14,"completion_tokens_details":{"reasoning_tokens":7}}}`

func reasoningAssertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("JSON = %s; want %s", got, want)
	}
}

func TestExecutorResponseAggregatesReasoningWithoutAliasingOrVisibleLeakage(t *testing.T) {
	for _, width := range []int{1, 7, 8192} {
		t.Run(fmt.Sprintf("chunk=%d", width), func(t *testing.T) {
			text := responseFrame(reasoningFirst) + responseFrame(reasoningSecond) + responseFrame(reasoningLast) + responseFrame(reasoningUsage) + responseFrame("[DONE]")
			payload, h, err := responseExecute(t, responseChunks(text, width), false)
			if err != nil {
				t.Fatal(err)
			}
			reasoningAssertJSON(t, payload, `{"id":"reason-chat","object":"chat.completion","model":"wb-ai/custom:model|variant","choices":[{"index":0,"message":{"role":"assistant","content":"答案","reasoning_content":"思考🌍","reasoning":"独立","reasoning_details":[{"index":3,"id":"thought-3","type":"reasoning.text","text":"考え🌍","signature":"sig-tail","provider":{"version":1,"tags":["opaque"]}},{"index":4,"id":"summary-id","type":"reasoning.summary","summary":"要約","format":"provider-v1"},{"index":7,"id":"encrypted","type":"reasoning.encrypted","data":"cipher-text","custom":{"a":[1,true,null]}}],"tool_calls":[{"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"世界\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":9,"total_tokens":14,"completion_tokens_details":{"reasoning_tokens":7}}}`)
			if h.upstreamClosed != 1 || h.callbackError != nil {
				t.Fatalf("upstream closes=%d; callback error=%v", h.upstreamClosed, h.callbackError)
			}
		})
	}
}

func TestExecutorResponsePreservesReasoningStreamEventsAndUsageOrder(t *testing.T) {
	frames := []string{reasoningFirst, reasoningSecond, reasoningLast, reasoningUsage}
	text := "id: reasoning-event\nevent: message\n"
	for _, frame := range frames {
		text += responseFrame(frame)
	}
	text += responseFrame("[DONE]")
	_, h, err := responseExecute(t, responseChunks(text, 1), true)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.streamError != "" || len(h.emitted) != 5 || h.upstreamClosed != 1 || h.callbackError != nil {
		t.Fatalf("events=%d; error=%q; closes=%d; callback=%v", len(h.emitted), h.streamError, h.upstreamClosed, h.callbackError)
	}
	for i, original := range frames {
		if i == 0 && !strings.HasPrefix(string(h.emitted[i]), "id: reasoning-event\nevent: message\n") {
			t.Fatal("reasoning event lost SSE metadata")
		}
		_, raw, ok := strings.Cut(string(h.emitted[i]), "data: ")
		if !ok {
			t.Fatalf("event %d has no data", i)
		}
		var expected map[string]any
		if err := json.Unmarshal([]byte(original), &expected); err != nil {
			t.Fatal(err)
		}
		expected["model"] = "wb-ai/custom:model|variant"
		want, err := json.Marshal(expected)
		if err != nil {
			t.Fatal(err)
		}
		reasoningAssertJSON(t, []byte(raw), string(want))
	}
	if string(h.emitted[4]) != "data: [DONE]\n\n" {
		t.Fatal("reasoning stream lost DONE")
	}
}

func TestExecutorResponseKeepsReasoningAliasesAndAnonymousDetailsIndependent(t *testing.T) {
	cases := []struct{ name, deltas, message string }{
		{"reasoning-content", `{"reasoning_content":"think"}`, `{"role":"assistant","content":"","reasoning_content":"think"}`},
		{"reasoning", `{"reasoning":"think"}`, `{"role":"assistant","content":"","reasoning":"think"}`},
		{"details", `{"reasoning_details":[{"type":"reasoning.text","text":"first"},{"type":"reasoning.text","text":"second"},{"type":"reasoning.summary","summary":"summary","signature":"opaque"}]}`, `{"role":"assistant","content":"","reasoning_details":[{"type":"reasoning.text","text":"first"},{"type":"reasoning.text","text":"second"},{"type":"reasoning.summary","summary":"summary","signature":"opaque"}]}`},
		{"empty", `{"reasoning_content":"","reasoning":"","reasoning_details":[]}`, `{"role":"assistant","content":"","reasoning_content":"","reasoning":"","reasoning_details":[]}`},
		{"null", `{"reasoning_content":null,"reasoning":null,"reasoning_details":null}`, `{"role":"assistant","content":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := responseFrame(`{"choices":[{"index":0,"delta":`+tc.deltas+`,"finish_reason":"stop"}]}`) + responseFrame(`{"choices":[{"index":0,"delta":null}]}`) + responseFrame(`{"choices":[{"index":0,"delta":{"reasoning":null,"reasoning_content":"","reasoning_details":[]}}]}`) + responseFrame("[DONE]")
			// The final empty string is an explicit reasoning_content field.
			// Keep the main assertion separate so null fields do not invent aliases.
			initial := responseFrame(`{"choices":[{"index":0,"delta":`+tc.deltas+`,"finish_reason":"stop"}]}`) + responseFrame("[DONE]")
			payload, _, err := responseExecute(t, responseChunks(initial, 5), false)
			if err != nil {
				t.Fatal(err)
			}
			reasoningAssertJSON(t, payload, `{"object":"chat.completion","model":"wb-ai/custom:model|variant","choices":[{"index":0,"message":`+tc.message+`,"finish_reason":"stop"}]}`)
			_, h, err := responseExecute(t, responseChunks(text, 5), true)
			if err != nil {
				t.Fatal(err)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.streamError != "" || len(h.emitted) != 4 {
				t.Fatalf("empty/null frames failed: events=%d, error=%q", len(h.emitted), h.streamError)
			}
			_, raw, _ := strings.Cut(string(h.emitted[1]), "data: ")
			reasoningAssertJSON(t, []byte(raw), `{"model":"wb-ai/custom:model|variant","choices":[{"index":0,"delta":null}]}`)
			_, raw, _ = strings.Cut(string(h.emitted[2]), "data: ")
			reasoningAssertJSON(t, []byte(raw), `{"model":"wb-ai/custom:model|variant","choices":[{"index":0,"delta":{"reasoning":null,"reasoning_content":"","reasoning_details":[]}}]}`)
		})
	}
}

func reasoningAssertFailure(t *testing.T, chunks [][]byte, streaming bool, code string) {
	t.Helper()
	_, h, err := responseExecute(t, chunks, streaming)
	if streaming && err == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.streamError == "" || strings.Contains(h.streamError, "private-reasoning-secret") || h.upstreamClosed != 1 {
			t.Fatalf("stream failure=%q; closes=%d", h.streamError, h.upstreamClosed)
		}
		for _, frame := range h.emitted {
			if string(frame) == "data: [DONE]\n\n" {
				t.Fatal("invalid reasoning emitted success DONE")
			}
		}
		return
	}
	var detail *workbuddy.Error
	if !errors.As(err, &detail) || detail.Code != code || strings.Contains(detail.Message, "private-reasoning-secret") {
		t.Fatalf("error=%v; want safe %s", err, code)
	}
	if h.upstreamClosed != 1 {
		t.Fatalf("upstream closes=%d", h.upstreamClosed)
	}
}

func TestExecutorResponseRejectsMalformedReasoningFragments(t *testing.T) {
	cases := []struct{ name, first, next string }{
		{"reasoning-content-object", `{"reasoning_content":{"text":"private-reasoning-secret"}}`, ""},
		{"reasoning-array", `{"reasoning":["private-reasoning-secret"]}`, ""},
		{"details-object", `{"reasoning_details":{"text":"private-reasoning-secret"}}`, ""},
		{"details-string", `{"reasoning_details":"private-reasoning-secret"}`, ""},
		{"null-detail", `{"reasoning_details":[null]}`, ""},
		{"nonobject-detail", `{"reasoning_details":["private-reasoning-secret"]}`, ""},
		{"negative-index", `{"reasoning_details":[{"index":-1,"text":"private-reasoning-secret"}]}`, ""},
		{"fractional-index", `{"reasoning_details":[{"index":0.5,"text":"private-reasoning-secret"}]}`, ""},
		{"string-index", `{"reasoning_details":[{"index":"0","text":"private-reasoning-secret"}]}`, ""},
		{"bad-id", `{"reasoning_details":[{"id":1}]}`, ""},
		{"bad-type", `{"reasoning_details":[{"type":true}]}`, ""},
		{"bad-text", `{"reasoning_details":[{"text":{}}]}`, ""},
		{"bad-summary", `{"reasoning_details":[{"summary":[]}]}`, ""},
		{"bad-signature", `{"reasoning_details":[{"signature":12}]}`, ""},
		{"bad-encrypted-data", `{"reasoning_details":[{"data":{}}]}`, ""},
		{"changed-id", `{"reasoning_details":[{"index":0,"id":"one","text":"private-reasoning-secret"}]}`, `{"reasoning_details":[{"index":0,"id":"two"}]}`},
		{"changed-index", `{"reasoning_details":[{"index":0,"id":"one"}]}`, `{"reasoning_details":[{"index":1,"id":"one"}]}`},
		{"changed-type", `{"reasoning_details":[{"id":"one","type":"reasoning.text"}]}`, `{"reasoning_details":[{"id":"one","type":"reasoning.summary"}]}`},
		{"changed-opaque-metadata", `{"reasoning_details":[{"index":0,"opaque":{"version":1}}]}`, `{"reasoning_details":[{"index":0,"opaque":{"version":2}}]}`},
		{"conflicting-identities", `{"reasoning_details":[{"index":0,"id":"a"},{"index":1,"id":"b"}]}`, `{"reasoning_details":[{"index":0,"id":"b"}]}`},
	}
	for _, tc := range cases {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, streaming), func(t *testing.T) {
				text := responseFrame(`{"choices":[{"index":0,"delta":` + tc.first + `,"finish_reason":null}]}`)
				if tc.next != "" {
					text += responseFrame(`{"choices":[{"index":0,"delta":` + tc.next + `,"finish_reason":null}]}`)
				}
				text += responseFrame(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`) + responseFrame("[DONE]")
				reasoningAssertFailure(t, responseChunks(text, 31), streaming, "upstream_stream_malformed")
			})
		}
	}
}

func TestExecutorResponseRejectsReasoningAfterChoiceFinished(t *testing.T) {
	for _, delta := range []string{`{"reasoning_content":"late"}`, `{"reasoning":"late"}`, `{"reasoning_details":[{"index":0,"text":"late"}]}`} {
		for _, streaming := range []bool{false, true} {
			text := responseFrame(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`) + responseFrame(`{"choices":[{"index":0,"delta":`+delta+`}]}`) + responseFrame("[DONE]")
			reasoningAssertFailure(t, responseChunks(text, 41), streaming, "upstream_stream_malformed")
		}
	}
}

func TestExecutorResponseBoundsReasoningAggregationAndDetailState(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning", "text", "summary", "signature", "data", "opaque"} {
		t.Run(field, func(t *testing.T) {
			// Each frame fits the 1 MiB event limit. HTML escaping expands its
			// semantic output sixfold, crossing 32 MiB after eleven fragments.
			fragment := strings.Repeat("<", 512<<10)
			delta := `{"` + field + `":"` + fragment + `"}`
			if field != "reasoning_content" && field != "reasoning" {
				identity := `"index":0,`
				if field == "opaque" {
					// Unidentified metadata remains distinct instead of overwriting.
					identity = ""
				}
				delta = `{"reasoning_details":[{` + identity + `"` + field + `":"` + fragment + `"}]}`
			}
			frame := []byte(responseFrame(`{"choices":[{"index":0,"delta":` + delta + `,"finish_reason":null}]}`))
			chunks := make([][]byte, 11)
			for i := range chunks {
				chunks[i] = frame
			}
			reasoningAssertFailure(t, chunks, false, "upstream_stream_too_large")
		})
	}
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("detail-allocations/stream=%v", streaming), func(t *testing.T) {
			// Indexed entries retain identity state in both response modes.
			// Account for those allocations even when no text is accumulated.
			chunks := make([][]byte, 33)
			for i := range chunks {
				var details strings.Builder
				for j := range 4096 {
					if j != 0 {
						details.WriteByte(',')
					}
					fmt.Fprintf(&details, `{"index":%d}`, i*4096+j)
				}
				chunks[i] = []byte(responseFrame(`{"choices":[{"index":0,"delta":{"reasoning_details":[` + details.String() + `]},"finish_reason":null}]}`))
			}
			reasoningAssertFailure(t, chunks, streaming, "upstream_stream_too_large")
		})
	}
}

func TestExecutorResponsePreservesNullableAndDeferredReasoningMetadata(t *testing.T) {
	text := responseFrame(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"index":0,"id":null,"type":null,"text":null,"summary":null,"signature":null,"opaque":null}]}}]}`) +
		responseFrame(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"index":0,"id":"thought","type":"reasoning.text","text":"","summary":"","signature":"","opaque":{"meaningful":[true,2]}}]}}]}`) +
		responseFrame(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"index":0,"id":"","type":"","text":null,"summary":null,"signature":null,"opaque":null}]},"finish_reason":"stop"}]}`) + responseFrame("[DONE]")
	payload, _, err := responseExecute(t, responseChunks(text, 3), false)
	if err != nil {
		t.Fatal(err)
	}
	reasoningAssertJSON(t, payload, `{"object":"chat.completion","model":"wb-ai/custom:model|variant","choices":[{"index":0,"message":{"role":"assistant","content":"","reasoning_details":[{"index":0,"id":"thought","type":"reasoning.text","text":"","summary":"","signature":"","opaque":{"meaningful":[true,2]}}]},"finish_reason":"stop"}]}`)
}

func TestExecutorResponseSharesReasoningTextAndToolAggregateBudget(t *testing.T) {
	fragment := strings.Repeat("<", 512<<10)
	content := []byte(responseFrame(`{"choices":[{"index":0,"delta":{"content":"` + fragment + `"}}]}`))
	reasoning := []byte(responseFrame(`{"choices":[{"index":0,"delta":{"reasoning":"` + fragment + `"}}]}`))
	tool := []byte(responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_budget","type":"function","function":{"name":"budget","arguments":"` + fragment + `"}}]}}]}`))
	var chunks [][]byte
	for range 4 {
		chunks = append(chunks, content, reasoning)
	}
	for range 3 {
		chunks = append(chunks, tool)
	}
	reasoningAssertFailure(t, chunks, false, "upstream_stream_too_large")
}
