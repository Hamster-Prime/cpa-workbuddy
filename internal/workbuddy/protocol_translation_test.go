package workbuddy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

var translationRequests = map[string]string{
	"openai":          `{"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}]}`,
	"openai-response": `{"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}]}`,
	"claude":          `{"max_tokens":2048,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]}`,
	"gemini":          `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}]}]}`,
}

func translationUpstream(native, text string) string {
	if native == "openai" {
		content, _ := json.Marshal(text)
		return responseFrame(`{"id":"cross-chat","object":"chat.completion.chunk","model":"common","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"考え","content":`+string(content)+`},"finish_reason":null}]}`) +
			responseFrame(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"世界\"}"}}]},"finish_reason":"tool_calls"}]}`) +
			responseFrame(`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":11,"total_tokens":23,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":2}}}`) + responseFrame("[DONE]")
	}
	content, _ := json.Marshal(text)
	return messagesResponseFrame("message_start", `{"type":"message_start","message":{"id":"cross-message","type":"message","role":"assistant","model":"claude-sonnet-5.5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":7,"output_tokens":0,"cache_creation_input_tokens":3,"cache_read_input_tokens":2,"reasoning_tokens":1}}}`) +
		messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`) +
		messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"考え"}}`) +
		messagesResponseFrame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"native-signature"}}`) +
		messagesResponseFrame("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":`+string(content)+`}}`) +
		messagesResponseFrame("content_block_stop", `{"type":"content_block_stop","index":1}`) +
		messagesResponseFrame("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_lookup","name":"lookup","input":{"q":"世界"}}}`) +
		messagesResponseFrame("content_block_stop", `{"type":"content_block_stop","index":2}`) +
		messagesResponseFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":11,"reasoning_tokens":2}}`) +
		messagesResponseFrame("message_stop", `{"type":"message_stop"}`)
}

func executeTranslation(t *testing.T, s *workbuddy.Service, req map[string]any, upstream string, streaming bool) ([]byte, *responseStreamHost) {
	t.Helper()
	h := &responseStreamHost{chunks: responseChunks(upstream, 1), closed: make(chan struct{})}
	method := "executor.execute"
	if streaming {
		method = "executor.execute_stream"
	}
	result, err := invoke(s, context.Background(), method, req, h.call)
	if err != nil {
		t.Fatal(err)
	}
	if streaming {
		select {
		case <-h.closed:
		case <-time.After(5 * time.Second):
			t.Fatal("translated response did not close")
		}
		if h.streamError != "" {
			t.Fatalf("translated stream failed: %s", h.streamError)
		}
		return nil, h
	}
	response := result.(workbuddy.ExecutorResponse)
	return response.Payload, h
}

func TestProtocolTranslationCrossesEveryClientAndModelProtocol(t *testing.T) {
	for _, native := range []string{"openai", "claude"} {
		for _, target := range []string{"openai", "openai-response", "claude", "gemini"} {
			for _, streaming := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-to-%s/stream=%v", native, target, streaming), func(t *testing.T) {
					s := workbuddy.New()
					defer s.Close()
					auth := parsedIdentity(t, "workbuddy", "")
					model := "wb/common"
					if native == "claude" {
						model = "wb/claude-sonnet-5.5"
					}
					req := map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": model, "Format": target, "SourceFormat": target, "Payload": []byte(translationRequests[target]), "OriginalRequest": []byte(translationRequests[target]), "Stream": streaming, "host_callback_id": "response-callback", "stream_id": "response-downstream"}
					payload, host := executeTranslation(t, s, req, translationUpstream(native, "Hello 世界"), streaming)
					if host.upstreamClosed != 1 || host.callbackError != nil {
						t.Fatalf("lease close=%d callback=%v", host.upstreamClosed, host.callbackError)
					}
					observation := translationObservation{tools: make(map[int]*translationTool)}
					if streaming {
						for _, frame := range host.emitted {
							observation.consumeFrame(t, target, frame)
						}
						if observation.terminal != 1 {
							t.Fatalf("terminal events=%d", observation.terminal)
						}
					} else {
						observation.consumeJSON(t, target, payload, false)
					}
					if observation.text != "Hello 世界" || observation.thinking != "考え" {
						t.Fatalf("text=%q thinking=%q", observation.text, observation.thinking)
					}
					if observation.input != 12 || observation.output != 11 {
						t.Fatalf("usage input=%v output=%v", observation.input, observation.output)
					}
					if observation.reasoning != 2 {
						t.Fatalf("reasoning usage=%v", observation.reasoning)
					}
					if len(observation.tools) != 1 {
						t.Fatalf("tools=%+v", observation.tools)
					}
					for _, tool := range observation.tools {
						if tool.name != "lookup" {
							t.Fatalf("tool name=%q", tool.name)
						}
						var args map[string]string
						if json.Unmarshal([]byte(tool.args), &args) != nil || args["q"] != "世界" {
							t.Fatalf("tool arguments=%q", tool.args)
						}
					}
				})
			}
		}
	}
}

type translationTool struct{ name, args string }
type translationObservation struct {
	text, thinking           string
	tools                    map[int]*translationTool
	input, output, reasoning float64
	terminal                 int
}

func (o *translationObservation) consumeFrame(t *testing.T, format string, frame []byte) {
	t.Helper()
	if !strings.HasSuffix(string(frame), "\n\n") {
		t.Fatalf("incomplete SSE record: %q", frame)
	}
	var data []string
	for line := range strings.SplitSeq(string(frame), "\n") {
		if content, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(content, " "))
		}
	}
	if len(data) != 1 {
		t.Fatalf("expected one SSE data record, got %q", frame)
	}
	if data[0] == "[DONE]" {
		if format != "openai" {
			t.Fatalf("unexpected DONE for %s", format)
		}
		o.terminal++
		return
	}
	o.consumeJSON(t, format, []byte(data[0]), true)
}

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func array(value any) []any           { result, _ := value.([]any); return result }
func stringValue(value any) string    { result, _ := value.(string); return result }
func number(value any) float64        { result, _ := value.(float64); return result }

func (o *translationObservation) consumeJSON(t *testing.T, format string, payload []byte, streaming bool) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(payload, &root); err != nil {
		t.Fatalf("invalid translated JSON: %v: %s", err, payload)
	}
	switch format {
	case "openai":
		for _, raw := range array(root["choices"]) {
			choice := object(raw)
			message := object(choice["message"])
			if streaming {
				message = object(choice["delta"])
			}
			o.text += stringValue(message["content"])
			o.thinking += stringValue(message["reasoning_content"])
			for i, rawTool := range array(message["tool_calls"]) {
				tool := object(rawTool)
				index := i
				if value, ok := tool["index"]; ok {
					index = int(number(value))
				}
				if o.tools[index] == nil {
					o.tools[index] = &translationTool{}
				}
				fn := object(tool["function"])
				o.tools[index].name += stringValue(fn["name"])
				o.tools[index].args += stringValue(fn["arguments"])
			}
		}
		if usage := object(root["usage"]); usage != nil {
			o.input = number(usage["prompt_tokens"])
			o.output = number(usage["completion_tokens"])
			o.reasoning = number(object(usage["completion_tokens_details"])["reasoning_tokens"])
		}
	case "claude":
		kind := stringValue(root["type"])
		if !streaming {
			for i, block := range array(root["content"]) {
				o.claudeBlock(object(block), i)
			}
		} else {
			switch kind {
			case "content_block_start":
				o.claudeBlock(object(root["content_block"]), int(number(root["index"])))
			case "content_block_delta":
				delta := object(root["delta"])
				o.text += stringValue(delta["text"])
				o.thinking += stringValue(delta["thinking"])
				if arg, ok := delta["partial_json"]; ok {
					index := int(number(root["index"]))
					if o.tools[index] == nil {
						o.tools[index] = &translationTool{}
					}
					o.tools[index].args += stringValue(arg)
				}
			case "message_stop":
				o.terminal++
			}
		}
		container := root
		if kind == "message_start" {
			container = object(root["message"])
		}
		if usage := object(container["usage"]); usage != nil {
			if _, ok := usage["input_tokens"]; ok {
				o.input = number(usage["input_tokens"]) + number(usage["cache_creation_input_tokens"]) + number(usage["cache_read_input_tokens"])
			}
			if _, ok := usage["output_tokens"]; ok {
				o.output = number(usage["output_tokens"])
			}
			if _, ok := usage["reasoning_tokens"]; ok {
				o.reasoning = number(usage["reasoning_tokens"])
			} else if details := object(usage["output_tokens_details"]); details != nil {
				o.reasoning = number(details["thinking_tokens"])
			}
		}
	case "openai-response":
		if streaming {
			if stringValue(root["type"]) != "response.completed" {
				return
			}
			o.terminal++
			root = object(root["response"])
		}
		for _, raw := range array(root["output"]) {
			item := object(raw)
			switch stringValue(item["type"]) {
			case "message":
				for _, rawPart := range array(item["content"]) {
					o.text += stringValue(object(rawPart)["text"])
				}
			case "reasoning":
				for _, rawPart := range array(item["summary"]) {
					o.thinking += stringValue(object(rawPart)["text"])
				}
			case "function_call":
				o.tools[len(o.tools)] = &translationTool{name: stringValue(item["name"]), args: stringValue(item["arguments"])}
			}
		}
		usage := object(root["usage"])
		o.input = number(usage["input_tokens"])
		o.output = number(usage["output_tokens"])
		o.reasoning = number(object(usage["output_tokens_details"])["reasoning_tokens"])
	case "gemini":
		for _, rawCandidate := range array(root["candidates"]) {
			candidate := object(rawCandidate)
			for _, rawPart := range array(object(candidate["content"])["parts"]) {
				part := object(rawPart)
				if thought, _ := part["thought"].(bool); thought {
					o.thinking += stringValue(part["text"])
				} else {
					o.text += stringValue(part["text"])
				}
				if fn := object(part["functionCall"]); fn != nil {
					args, _ := json.Marshal(fn["args"])
					o.tools[len(o.tools)] = &translationTool{name: stringValue(fn["name"]), args: string(args)}
				}
			}
			if stringValue(candidate["finishReason"]) != "" {
				o.terminal++
			}
		}
		if usage := object(root["usageMetadata"]); usage != nil {
			if _, ok := usage["promptTokenCount"]; ok {
				o.input = number(usage["promptTokenCount"])
				o.output = number(usage["candidatesTokenCount"])
				o.reasoning = number(usage["thoughtsTokenCount"])
			}
		}
	}
}

func (o *translationObservation) claudeBlock(block map[string]any, index int) {
	switch stringValue(block["type"]) {
	case "text":
		o.text += stringValue(block["text"])
	case "thinking":
		o.thinking += stringValue(block["thinking"])
	case "tool_use":
		args := ""
		if input := object(block["input"]); len(input) > 0 {
			raw, _ := json.Marshal(input)
			args = string(raw)
		}
		o.tools[index] = &translationTool{name: stringValue(block["name"]), args: args}
	}
}

func TestProtocolTranslationUsesSourceFormatAndKeepsRequestStateLocal(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	for _, text := range []string{"first request", "second request"} {
		req := map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": "wb/common", "Format": "openai-response", "SourceFormat": "claude", "Payload": []byte(translationRequests["claude"]), "OriginalRequest": []byte(translationRequests["openai-response"]), "host_callback_id": "response-callback", "stream_id": "response-downstream"}
		_, host := executeTranslation(t, s, req, translationUpstream("openai", text), true)
		observation := translationObservation{tools: make(map[int]*translationTool)}
		for _, frame := range host.emitted {
			observation.consumeFrame(t, "openai-response", frame)
		}
		if observation.text != text || observation.terminal != 1 || len(observation.tools) != 1 {
			t.Fatalf("request-local state: %+v", observation)
		}
	}
}

func TestProtocolTranslationPreservesImagesToolsAndReasoningAtUpstreamBoundary(t *testing.T) {
	inputs := map[string]string{
		"openai":          `{"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}}]}],"reasoning_effort":"high","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}]}`,
		"openai-response": `{"input":[{"role":"user","content":[{"type":"input_text","text":"describe"},{"type":"input_image","image_url":"data:image/png;base64,aGk="}]}],"reasoning":{"effort":"high"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}]}`,
		"claude":          `{"max_tokens":32000,"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}]}],"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]}`,
		"gemini":          `{"contents":[{"role":"user","parts":[{"text":"describe"},{"inlineData":{"mimeType":"image/png","data":"aGk="}}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}},"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}]}]}`,
	}
	for _, native := range []string{"openai", "claude"} {
		for source, payload := range inputs {
			t.Run(native+"/"+source, func(t *testing.T) {
				s := workbuddy.New()
				defer s.Close()
				auth, _ := discoverChatCapabilities(t, "workbuddy", "translation-vision", chatCapabilityConfig)
				model := "wb/common"
				if native == "claude" {
					model = "wb/claude-opus-5.5"
				}
				h := &responseStreamHost{chunks: responseChunks(translationUpstream(native, "image answer"), 7), closed: make(chan struct{})}
				var sent httpRequest
				req := map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": model, "Format": source, "SourceFormat": source, "Payload": []byte(payload), "OriginalRequest": []byte(payload), "host_callback_id": "response-callback"}
				_, err := invoke(s, context.Background(), "executor.execute", req, func(method string, raw []byte) ([]byte, error) {
					if method == "host.http.do_stream" {
						if err := json.Unmarshal(raw, &sent); err != nil {
							return nil, err
						}
					}
					return h.call(method, raw)
				})
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]json.RawMessage
				if native == "claude" {
					body = messagesRequestBody(t, sent)
					if sent.URL != "https://copilot.tencent.com/v1/messages" || !strings.Contains(string(body["messages"]), `"media_type":"image/png"`) || !strings.Contains(string(body["messages"]), `"data":"aGk="`) || len(body["thinking"]) == 0 {
						t.Fatalf("Messages image/thinking request=%s", sent.Body)
					}
				} else {
					body = requestProtocolBody(t, sent)
					if sent.URL != "https://copilot.tencent.com/v2/chat/completions" || !strings.Contains(string(body["messages"]), "data:image/png;base64,aGk=") || !strings.Contains(string(body["reasoning_effort"]), "high") {
						t.Fatalf("Chat image/thinking request=%v", body)
					}
				}
				if !strings.Contains(string(body["tools"]), `"name":"lookup"`) || !strings.Contains(string(body["tools"]), `"q"`) {
					t.Fatalf("tool schema=%s", body["tools"])
				}
				// The same translated image must still pass the existing per-account
				// capability check, rather than gaining access through a new schema.
				unadvertised := parsedIdentity(t, "workbuddy", "translation-text")
				req["AuthID"], req["StorageJSON"] = unadvertised.ID, unadvertised.StorageJSON
				_, err = invoke(s, context.Background(), "executor.execute", req, func(string, []byte) ([]byte, error) {
					t.Fatal("unadvertised translated image reached upstream")
					return nil, nil
				})
				if err == nil {
					t.Fatal("translated image bypassed account capabilities")
				}
			})
		}
	}
}

func TestProtocolTranslationKeepsIndependentChatReasoningAliases(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", streaming), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy", "")
			req := map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": "wb/common", "Format": "gemini", "SourceFormat": "gemini", "Payload": []byte(translationRequests["gemini"]), "stream_id": "response-downstream"}
			upstream := strings.Replace(translationUpstream("openai", "answer"), `"reasoning_content":"考え"`, `"reasoning_content":"考え","reasoning":"独立","reasoning_details":[{"type":"reasoning.summary","summary":"要約"}]`, 1)
			payload, host := executeTranslation(t, s, req, upstream, streaming)
			observation := translationObservation{tools: make(map[int]*translationTool)}
			if streaming {
				for _, frame := range host.emitted {
					observation.consumeFrame(t, "gemini", frame)
				}
			} else {
				observation.consumeJSON(t, "gemini", payload, false)
			}
			if observation.text != "answer" || observation.thinking != "考え独立要約" {
				t.Fatalf("reasoning aliases: text=%q thinking=%q", observation.text, observation.thinking)
			}
		})
	}
}

func TestProtocolTranslationRetainsMessagesLengthTerminationAndLateUsage(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", streaming), func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy", "")
			req := map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": "wb/claude-sonnet-5.5", "Format": "gemini", "SourceFormat": "gemini", "Payload": []byte(translationRequests["gemini"]), "stream_id": "response-downstream"}
			upstream := strings.Replace(translationUpstream("claude", "answer"), `"stop_reason":"tool_use"`, `"stop_reason":"max_tokens"`, 1)
			payload, host := executeTranslation(t, s, req, upstream, streaming)
			var records [][]byte
			if streaming {
				for _, frame := range host.emitted {
					_, data, ok := strings.Cut(string(frame), "data: ")
					if !ok {
						t.Fatalf("missing SSE data: %q", frame)
					}
					records = append(records, []byte(data))
				}
			} else {
				records = append(records, payload)
			}
			finishes := 0
			for _, record := range records {
				var root map[string]any
				if err := json.Unmarshal(record, &root); err != nil {
					t.Fatal(err)
				}
				for _, raw := range array(root["candidates"]) {
					if finish := stringValue(object(raw)["finishReason"]); finish != "" {
						finishes++
						if finish != "MAX_TOKENS" {
							t.Fatalf("length termination became %q", finish)
						}
						usage := object(root["usageMetadata"])
						if number(usage["promptTokenCount"]) != 12 || number(usage["candidatesTokenCount"]) != 11 || number(usage["thoughtsTokenCount"]) != 2 {
							t.Fatalf("terminal usage=%v", usage)
						}
					}
				}
			}
			if finishes != 1 {
				t.Fatalf("terminal candidate count=%d", finishes)
			}
		})
	}
}

func TestProtocolTranslationValidatesOriginalEffortAgainstAccount(t *testing.T) {
	inputs := map[string]string{
		"openai":          `{"messages":[{"role":"user","content":"hello"}],"reasoning":{"effort":"low"}}`,
		"openai-response": `{"input":"hello","reasoning":{"effort":"low"}}`,
		"gemini":          `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"low"}}}`,
	}
	for source, payload := range inputs {
		t.Run(source, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth, _ := discoverChatCapabilities(t, "workbuddy", "translation-effort", chatCapabilityConfig)
			_, err := invoke(s, context.Background(), "executor.execute", map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": "wb/claude-opus-5.5", "Format": source, "SourceFormat": source, "Payload": []byte(payload)}, func(string, []byte) ([]byte, error) { t.Fatal("unsupported effort reached upstream"); return nil, nil })
			if err == nil {
				t.Fatal("effort-to-budget translation bypassed account effort set")
			}
		})
	}
}

func TestProtocolTranslationRestoresToolNamesBeforeClientConversion(t *testing.T) {
	for _, target := range []string{"openai-response", "claude", "gemini"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", target, streaming), func(t *testing.T) {
				s := workbuddy.New()
				defer s.Close()
				auth := parsedIdentity(t, "workbuddy", "")
				payload := strings.ReplaceAll(translationRequests[target], `"lookup"`, `"fs.read"`)
				req := map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": "wb/common", "Format": target, "SourceFormat": target, "Payload": []byte(payload), "OriginalRequest": []byte(payload), "stream_id": "response-downstream"}
				upstream := strings.ReplaceAll(translationUpstream("openai", "answer"), `"name":"lookup"`, `"name":"fs_read"`)
				response, host := executeTranslation(t, s, req, upstream, streaming)
				observation := translationObservation{tools: make(map[int]*translationTool)}
				if streaming {
					for _, frame := range host.emitted {
						observation.consumeFrame(t, target, frame)
					}
				} else {
					observation.consumeJSON(t, target, response, false)
				}
				if len(observation.tools) != 1 {
					t.Fatalf("tools=%v", observation.tools)
				}
				for _, tool := range observation.tools {
					if tool.name != "fs.read" {
						t.Fatalf("client received fitted upstream name %q", tool.name)
					}
				}
			})
		}
	}
}

func TestProtocolTranslationDoesNotSilentlyDropImageOutputRequests(t *testing.T) {
	for source, payload := range map[string]string{
		"openai":          `{"messages":[{"role":"user","content":"draw"}],"modalities":["image"]}`,
		"openai-response": `{"input":"draw","tools":[{"type":"image_generation"}]}`,
		"gemini":          `{"contents":[{"role":"user","parts":[{"text":"draw"}]}],"generationConfig":{"responseModalities":["IMAGE"]}}`,
	} {
		t.Run(source, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy", "")
			_, err := invoke(s, context.Background(), "executor.execute", map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": "wb/claude-sonnet-5.5", "Format": source, "SourceFormat": source, "Payload": []byte(payload)}, func(string, []byte) ([]byte, error) {
				t.Fatal("image generation was silently translated to text")
				return nil, nil
			})
			if err == nil {
				t.Fatal("unsupported output modality was accepted")
			}
		})
	}
}
