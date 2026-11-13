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

const collisionTools = `[{"type":"function","function":{"name":"fs.read","parameters":{"type":"object","properties":{}}}},{"type":"function","function":{"name":"fs_read","parameters":{"type":"object","properties":{}}}},{"type":"function","function":{"name":"fs/read","parameters":{"type":"object","properties":{}}}},{"type":"function","function":{"name":"搜索","parameters":{"type":"object","properties":{}}}}]`

func TestChatToolsNamesChoicesAndHistoryStayReversible(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	for _, choice := range []string{`"fs.read"`, `{"type":"function","function":{"name":"fs.read"}}`, `"auto"`, `{"type":"required"}`} {
		payload := []byte(`{"messages":[{"role":"user","content":"read"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"fs.read","arguments":"{}"}}]},{"role":"tool","name":"fs.read","tool_call_id":"c1","content":"done"}],"tools":` + collisionTools + `,"tool_choice":` + choice + `}`)
		body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/glm-5.3", payload)))
		var tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if err := json.Unmarshal(body["tools"], &tools); err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(tools))
		for i, tool := range tools {
			got[i] = tool.Function.Name
		}
		if !reflect.DeepEqual(got, []string{"fs_read_2", "fs_read", "fs_read_3", "__"}) {
			t.Fatalf("collision mapping = %v", got)
		}
		wantChoice := `"fs_read_2"`
		if strings.Contains(choice, "auto") {
			wantChoice = `"auto"`
		}
		if strings.Contains(choice, "required") {
			wantChoice = `"required"`
		}
		if string(body["tool_choice"]) != wantChoice {
			t.Fatalf("selection = %s, want %s", body["tool_choice"], wantChoice)
		}
		var messages []map[string]any
		if err := json.Unmarshal(body["messages"], &messages); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body["messages"]), `"name":"fs_read_2"`) || messages[3]["name"] != "fs_read_2" || messages[3]["tool_call_id"] != "c1" {
			t.Fatalf("history = %s", body["messages"])
		}
	}
}

func TestChatToolsCleanDefinitionsAndExplicitSelection(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy-ai", "")
	clean := `[{"function":{"description":"kept","name":"mcp__node_repl__js","parameters":{"properties":{"code":{"pattern":"^x","type":"string"}},"required":["code"],"type":"object"}},"type":"function"}]`
	payload := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":` + clean + `,"tool_choice":"required","parallel_tool_calls":true}`)
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb-ai/glm-5.3", payload)))
	if string(body["tools"]) != clean || string(body["tool_choice"]) != `"required"` || string(body["parallel_tool_calls"]) != "true" {
		t.Fatalf("clean tools changed = %s", body["tools"])
	}
	for _, choice := range []string{`null`, `false`, `[]`, `{}`, `{"type":"unknown"}`, `{"type":"function"}`, `"missing"`} {
		_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb-ai/glm-5.3", []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":`+clean+`,"tool_choice":`+choice+`}`)), func(string, []byte) ([]byte, error) {
			t.Error("invalid selection reached HTTP")
			return nil, errors.New("unexpected callback")
		})
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "invalid_request" {
			t.Fatalf("choice %s error = %v", choice, err)
		}
	}
}

func executeToolResponse(t *testing.T, text string, streaming bool, width int) ([]byte, *responseStreamHost, error) {
	t.Helper()
	s := workbuddy.New()
	t.Cleanup(s.Close)
	auth := parsedIdentity(t, "workbuddy-ai", "")
	h := &responseStreamHost{chunks: responseChunks(text, width), closed: make(chan struct{})}
	req := requestProtocolInput(auth, "wb-ai/glm-5.3", []byte(`{"messages":[{"role":"user","content":"read"}],"tools":`+collisionTools+`}`))
	req["stream_id"] = "response-downstream"
	method := "executor.execute"
	if streaming {
		method = "executor.execute_stream"
	}
	result, err := invoke(s, context.Background(), method, req, h.call)
	if err != nil {
		return nil, h, err
	}
	if streaming {
		select {
		case <-h.closed:
		case <-time.After(5 * time.Second):
			t.Fatal("tool stream did not close")
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

func TestChatToolsInterleavedFragmentsRestoreNamesAndAggregate(t *testing.T) {
	text := responseFrame(`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":2,"id":"call-search","type":"function","function":{"name":"__","arguments":"{\"query\":\"世"}},{"index":0,"id":"call-read","type":"function","function":{"name":"fs_read_2","arguments":"{\"path\":"}}]},"finish_reason":null}]}`) + responseFrame(`{"choices":[{"index":0,"delta":{},"finish_reason":null}]}`) + responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}},{"index":2,"function":{"arguments":"界🙂\"}"}}]},"finish_reason":null}]}`) + responseFrame(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`) + responseFrame(`{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`) + responseFrame("[DONE]")
	for _, streaming := range []bool{false, true} {
		for _, width := range []int{1, 19, 8192} {
			t.Run(fmt.Sprintf("stream=%v/width=%d", streaming, width), func(t *testing.T) {
				payload, h, err := executeToolResponse(t, text, streaming, width)
				if err != nil {
					t.Fatal(err)
				}
				if streaming {
					h.mu.Lock()
					defer h.mu.Unlock()
					joined := string(bytesJoinToolEvents(h.emitted))
					if h.streamError != "" || len(h.emitted) != 6 || !strings.Contains(joined, `"name":"fs.read"`) || !strings.Contains(joined, `"name":"搜索"`) || strings.Contains(joined, "fs_read_2") || !strings.HasSuffix(joined, "data: [DONE]\n\n") {
						t.Fatalf("stream error %q events %s", h.streamError, joined)
					}
					return
				}
				var got any
				if err := json.Unmarshal(payload, &got); err != nil {
					t.Fatal(err)
				}
				var want any
				if err := json.Unmarshal([]byte(`{"model":"wb-ai/glm-5.3","object":"chat.completion","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-read","type":"function","function":{"name":"fs.read","arguments":"{\"path\":\"a.txt\"}"}},{"id":"call-search","type":"function","function":{"name":"搜索","arguments":"{\"query\":\"世界🙂\"}"}}]}}],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("completion = %s", payload)
				}
			})
		}
	}
}

func bytesJoinToolEvents(events [][]byte) []byte {
	var all []byte
	for _, event := range events {
		all = append(all, event...)
	}
	return all
}

func TestChatToolsMalformedOrIncompleteCallsFail(t *testing.T) {
	for _, delta := range []string{
		`[{"index":-1}]`,
		`[{"index":0,"id":"a","function":{"name":"fs_read_2","arguments":7}}]`,
		`[{"index":0,"id":"a","function":{"arguments":"{}"}}]`,
		`[{"index":0,"id":"a","function":{"name":"fs_read_2"}},{"index":1,"id":"a","function":{"name":"fs_read"}}]`,
		`[{"index":0,"id":"a","function":{"name":"fs_read_2"}},{"index":0,"id":"b","function":{"name":"fs_read"}}]`,
	} {
		text := responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":`+delta+`},"finish_reason":"tool_calls"}]}`) + responseFrame("[DONE]")
		_, _, err := executeToolResponse(t, text, false, 7)
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "upstream_stream_malformed" {
			t.Fatalf("delta %s error %v", delta, err)
		}
	}
}

func TestChatToolsDefinitionOrderKeepsHistoricalNames(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	var tools []map[string]any
	if err := json.Unmarshal([]byte(collisionTools), &tools); err != nil {
		t.Fatal(err)
	}
	tools[0], tools[3] = tools[3], tools[0]
	tools[1], tools[2] = tools[2], tools[1]
	payload := []byte(mustRequestProtocolJSON(t, map[string]any{"messages": []map[string]string{{"role": "user", "content": "read"}}, "tools": tools}))
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/glm-5.3", payload)))
	var sent []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(body["tools"], &sent); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"__", "fs_read_3", "fs_read", "fs_read_2"} {
		if sent[i].Function.Name != want {
			t.Fatalf("reordered name %d = %s, want %s", i, sent[i].Function.Name, want)
		}
	}
}

func TestChatToolsRejectAmbiguousDefinitionsAndReservedForcedNames(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	for _, extra := range []string{
		`"tools":{}`, `"tools":false`, `"tools":[null]`,
		`"tools":[{"type":"function","function":{"name":"x"}},{"type":"function","function":{"name":"x"}}]`,
		`"tools":[{"type":"function","function":{"name":"none"}}],"tool_choice":{"type":"function","function":{"name":"none"}}`,
		`"tools":[{"type":"function","function":{"name":"auto"}}],"tool_choice":{"type":"function","function":{"name":"auto"}}`,
	} {
		_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb/glm-5.3", []byte(`{"messages":[{"role":"user","content":"hi"}],`+extra+`}`)), func(string, []byte) ([]byte, error) {
			t.Error("ambiguous tools reached HTTP")
			return nil, errors.New("unexpected callback")
		})
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "invalid_request" {
			t.Fatalf("definition %s error = %v", extra, err)
		}
	}
}

func TestChatToolsArgumentsRespectAggregateLimit(t *testing.T) {
	first := []byte(responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-large","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}]}`))
	fragment := []byte(responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"` + strings.Repeat("a", 512000) + `"}}]},"finish_reason":null}]}`))
	chunks := [][]byte{first}
	for range 67 {
		chunks = append(chunks, fragment)
	}
	_, host, err := responseExecute(t, chunks, false)
	var detail *workbuddy.Error
	if !errors.As(err, &detail) || detail.Code != "upstream_stream_too_large" || host.upstreamClosed != 1 {
		t.Fatalf("large tool arguments error = %v, closes = %d", err, host.upstreamClosed)
	}
}

func TestChatToolsAcceptFunctionShapeAndRejectForcedNoneCase(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	payload := []byte(`{"messages":[{"role":"user","content":"read"}],"tools":[{"function":{"name":"read"}}]}`)
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/glm-5.3", payload)))
	if string(body["tools"]) != `[{"function":{"name":"read","parameters":{"type":"object","properties":{}}},"type":"function"}]` {
		t.Fatalf("function shape = %s", body["tools"])
	}
	for _, name := range []string{"NONE", "None"} {
		payload := []byte(mustRequestProtocolJSON(t, map[string]any{"messages": []map[string]string{{"role": "user", "content": "read"}}, "tools": []map[string]any{{"type": "function", "function": map[string]string{"name": name}}}, "tool_choice": map[string]any{"type": "function", "function": map[string]string{"name": name}}}))
		_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb/glm-5.3", payload), func(string, []byte) ([]byte, error) {
			t.Error("forced NONE reached HTTP")
			return nil, errors.New("unexpected callback")
		})
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "invalid_request" {
			t.Fatalf("forced %s error = %v", name, err)
		}
	}
}

func TestChatToolsSplitNamesAndEmptyMetadata(t *testing.T) {
	text := responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-name","type":"function","function":{"name":"fs_","arguments":"{\"path\":"}}]},"finish_reason":null}]}`) +
		responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":null,"type":"","function":{"name":"read_","arguments":null}}]},"finish_reason":null}]}`) +
		responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"","type":null,"function":null}]},"finish_reason":null}]}`) +
		responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"2","arguments":"\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}`) + responseFrame("[DONE]")
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			payload, host, err := executeToolResponse(t, text, streaming, 1)
			if err != nil {
				t.Fatal(err)
			}
			if streaming {
				host.mu.Lock()
				defer host.mu.Unlock()
				if host.streamError != "" {
					t.Fatal(host.streamError)
				}
				var name, arguments strings.Builder
				for _, frame := range host.emitted {
					data := strings.TrimSpace(strings.TrimPrefix(string(frame), "data: "))
					if data == "[DONE]" {
						continue
					}
					var event struct {
						Choices []struct {
							Delta struct {
								Tools []struct {
									Function struct {
										Name      string `json:"name"`
										Arguments string `json:"arguments"`
									} `json:"function"`
								} `json:"tool_calls"`
							} `json:"delta"`
						} `json:"choices"`
					}
					if err := json.Unmarshal([]byte(data), &event); err != nil {
						t.Fatal(err)
					}
					for _, choice := range event.Choices {
						for _, call := range choice.Delta.Tools {
							name.WriteString(call.Function.Name)
							arguments.WriteString(call.Function.Arguments)
						}
					}
				}
				if name.String() != "fs.read" || arguments.String() != `{"path":"a.txt"}` {
					t.Fatalf("name=%q arguments=%q", name.String(), arguments.String())
				}
				return
			}
			var completion struct {
				Choices []struct {
					Message struct {
						Tools []struct {
							ID       string `json:"id"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(payload, &completion); err != nil {
				t.Fatal(err)
			}
			if len(completion.Choices) != 1 || len(completion.Choices[0].Message.Tools) != 1 {
				t.Fatalf("completion=%s", payload)
			}
			call := completion.Choices[0].Message.Tools[0]
			if call.ID != "call-name" || call.Function.Name != "fs.read" || call.Function.Arguments != `{"path":"a.txt"}` {
				t.Fatalf("completion=%s", payload)
			}
		})
	}
}
