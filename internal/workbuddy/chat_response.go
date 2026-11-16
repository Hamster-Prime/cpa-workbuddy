package workbuddy

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	chatEventLimit     = 1 << 20
	chatAggregateLimit = 32 << 20
)

type chatChoice struct {
	fields    map[string]json.RawMessage
	content   strings.Builder
	refusal   strings.Builder
	role      string
	finished  bool
	tools     map[int]*chatToolCall
	reasoning *chatReasoningResponse
}

// consumeChatStream translates the upstream SSE stream without retaining its
// transport chunks. A terminal read may still contain the final event bytes.
func consumeChatStream(read func() ([]byte, bool, error), model string, toolNames map[string]string, emit func([]byte) error) ([]byte, error) {
	choices := make(map[int]*chatChoice)
	top := make(map[string]json.RawMessage)
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return nil, failure("invalid_request", "cannot encode the response model", 400)
	}
	var decoder eventStreamDecoder
	var done bool
	aggregateSize := 0
	malformed := func() error {
		return failure("upstream_stream_malformed", "WorkBuddy returned an invalid chat stream", 502)
	}
	incomplete := func() error {
		return failure("upstream_stream_incomplete", "WorkBuddy chat stream ended before completion", 502)
	}
	tooLarge := func() error {
		return failure("upstream_stream_too_large", "WorkBuddy chat stream exceeded the response limit", 502)
	}
	complete := func() bool {
		if len(choices) == 0 {
			return false
		}
		for _, choice := range choices {
			if !choice.finished {
				return false
			}
		}
		return true
	}
	processEvent := func(event streamEvent) error {
		eventName, fields, hasData := event.name, event.fields, event.hasData
		if !hasData {
			if eventName == "error" {
				return failure("upstream_stream_error", "WorkBuddy chat stream failed", 502)
			}
			return nil
		}
		payload := bytes.TrimSpace(event.data)
		if bytes.Equal(payload, []byte("[DONE]")) {
			if eventName == "error" {
				return failure("upstream_stream_error", "WorkBuddy chat stream failed", 502)
			}
			if !complete() {
				return incomplete()
			}
			done = true
			if emit != nil {
				return emit([]byte("data: [DONE]\n\n"))
			}
			return nil
		}
		var chunk map[string]json.RawMessage
		if !utf8.Valid(payload) || json.Unmarshal(payload, &chunk) != nil || chunk == nil {
			return malformed()
		}
		if eventName == "error" || chatHasJSONValue(chunk["error"]) || chatHasErrorCode(chunk["code"]) {
			if chatClientRejected(chunk) {
				return failure("upstream_client_version_rejected", "WorkBuddy client version rejected", 502)
			}
			return failure("upstream_stream_error", "WorkBuddy chat stream failed", 502)
		}
		var rawChoices []json.RawMessage
		if raw, exists := chunk["choices"]; !exists || json.Unmarshal(raw, &rawChoices) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return malformed()
		}
		seen := make(map[int]bool, len(rawChoices))
		for choiceIndex, rawChoice := range rawChoices {
			var wire map[string]json.RawMessage
			if json.Unmarshal(rawChoice, &wire) != nil || wire == nil {
				return malformed()
			}
			var index int
			if raw, ok := wire["index"]; !ok || json.Unmarshal(raw, &index) != nil || index < 0 || seen[index] {
				return malformed()
			}
			seen[index] = true
			choice := choices[index]
			if choice == nil {
				if len(choices) >= chatAggregateLimit/128 {
					return tooLarge()
				}
				aggregateSize += 128
				choice = &chatChoice{}
				if emit == nil {
					choice.fields = make(map[string]json.RawMessage)
				}
				choices[index] = choice
			}
			if chatHasJSONValue(wire["message"]) {
				return failure("unsupported_upstream_response", "WorkBuddy returned an unsupported chat response", 502)
			}
			var delta map[string]json.RawMessage
			if raw, exists := wire["delta"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				if json.Unmarshal(raw, &delta) != nil || delta == nil {
					return malformed()
				}
			}
			for key, raw := range delta {
				if key == "tool_calls" {
					if !chatHasJSONValue(raw) {
						continue
					}
					fitted, err := choice.addToolDeltas(raw, toolNames, emit == nil, &aggregateSize)
					if err != nil {
						return err
					}
					delta[key] = fitted
					continue
				}
				if key == "reasoning_content" || key == "reasoning" || key == "reasoning_details" {
					if err := choice.addReasoningDelta(key, raw, emit == nil, &aggregateSize); err != nil {
						return err
					}
					continue
				}
				if key == "function_call" && chatHasJSONValue(raw) && chatEmptyFunctionCall(raw) {
					continue
				}
				if key != "role" && key != "content" && key != "refusal" {
					if chatHasJSONValue(raw) && !bytes.Equal(raw, []byte(`""`)) && !bytes.Equal(raw, []byte("[]")) {
						return failure("unsupported_upstream_response", "WorkBuddy returned an unsupported chat response", 502)
					}
					continue
				}
				if !chatHasJSONValue(raw) {
					continue
				}
				var text string
				if json.Unmarshal(raw, &text) != nil || choice.finished && text != "" {
					return malformed()
				}
				if emit == nil {
					aggregateSize += chatEncodedTextSize(text)
					if aggregateSize > chatAggregateLimit {
						return tooLarge()
					}
					switch key {
					case "content":
						choice.content.WriteString(text)
					case "refusal":
						choice.refusal.WriteString(text)
					case "role":
						choice.role = text
					}
				}
			}
			if raw := wire["finish_reason"]; chatHasFinishReason(raw) {
				var reason string
				if json.Unmarshal(raw, &reason) != nil || choice.finished {
					return malformed()
				}
				if reason == "function_call" {
					return failure("unsupported_upstream_response", "WorkBuddy returned legacy function calls", 502)
				}
				if !choice.completeTools() || reason == "tool_calls" && len(choice.tools) == 0 {
					return malformed()
				}
				if emit != nil && len(toolNames) > 0 && len(choice.tools) > 0 {
					if delta == nil {
						delta = make(map[string]json.RawMessage)
					}
					if err := choice.finishToolNames(delta, toolNames); err != nil {
						return err
					}
				}
				choice.finished = true
			}
			if emit == nil {
				for key, value := range wire {
					if key != "delta" && (key != "finish_reason" || chatHasFinishReason(value)) {
						if _, exists := choice.fields[key]; !exists {
							aggregateSize += len(key) + 64
						}
						aggregateSize += len(value) - len(choice.fields[key])
						choice.fields[key] = value
					}
				}
			}
			if emit != nil && len(toolNames) > 0 && delta != nil {
				encoded, err := json.Marshal(delta)
				if err != nil {
					return malformed()
				}
				wire["delta"] = encoded
				encoded, err = json.Marshal(wire)
				if err != nil {
					return malformed()
				}
				rawChoices[choiceIndex] = encoded
			}
		}
		if emit != nil {
			if len(toolNames) > 0 {
				encoded, err := json.Marshal(rawChoices)
				if err != nil {
					return malformed()
				}
				chunk["choices"] = encoded
			}
			chunk["model"] = modelJSON
			raw, err := json.Marshal(chunk)
			if err != nil {
				return malformed()
			}
			frame := make([]byte, 0, len(fields)+len(raw)+8)
			frame = append(frame, fields...)
			frame = append(frame, "data: "...)
			frame = append(frame, raw...)
			frame = append(frame, '\n', '\n')
			return emit(frame)
		}
		for key, value := range chunk {
			if key != "choices" && key != "model" && key != "object" {
				if _, exists := top[key]; !exists {
					aggregateSize += len(key) + 64
				}
				aggregateSize += len(value) - len(top[key])
				top[key] = value
			}
		}
		if aggregateSize > chatAggregateLimit {
			return tooLarge()
		}
		return nil
	}
	for !done {
		payload, terminal, readErr := read()
		_, err := decoder.feed(payload, func(event streamEvent) (bool, error) {
			err := processEvent(event)
			return done, err
		}, tooLarge)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if terminal {
			return nil, incomplete()
		}
	}
	if emit != nil {
		return nil, nil
	}
	indexes := make([]int, 0, len(choices))
	for index := range choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	outChoices := make([]map[string]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		choice := choices[index]
		role := choice.role
		if role == "" {
			role = "assistant"
		}
		message := map[string]any{"role": role, "content": choice.content.String()}
		if choice.refusal.Len() > 0 {
			message["refusal"] = choice.refusal.String()
		}
		if choice.reasoning != nil {
			choice.reasoning.addMessageFields(message)
		}
		if len(choice.tools) > 0 {
			message["tool_calls"] = choice.aggregatedTools(toolNames)
			if choice.content.Len() == 0 {
				message["content"] = nil
			}
		}
		messageJSON, err := json.Marshal(message)
		if err != nil {
			return nil, malformed()
		}
		choice.fields["message"] = messageJSON
		outChoices = append(outChoices, choice.fields)
	}
	choicesJSON, err := json.Marshal(outChoices)
	if err != nil {
		return nil, malformed()
	}
	top["choices"] = choicesJSON
	top["model"] = modelJSON
	top["object"] = json.RawMessage(`"chat.completion"`)
	result, err := json.Marshal(top)
	if err != nil {
		return nil, malformed()
	}
	if len(result) > chatAggregateLimit {
		return nil, tooLarge()
	}
	return result, nil
}

func chatHasJSONValue(raw json.RawMessage) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func chatHasFinishReason(raw json.RawMessage) bool {
	return chatHasJSONValue(raw) && !bytes.Equal(bytes.TrimSpace(raw), []byte(`""`))
}

func chatEmptyFunctionCall(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for key, value := range fields {
		if key != "name" && key != "arguments" {
			return false
		}
		var text string
		if json.Unmarshal(value, &text) != nil || text != "" {
			return false
		}
	}
	return true
}

func chatHasErrorCode(raw json.RawMessage) bool {
	return chatHasJSONValue(raw) && !bytes.Equal(bytes.TrimSpace(raw), []byte("0")) && !bytes.Equal(bytes.TrimSpace(raw), []byte(`"0"`))
}

func chatClientRejected(value map[string]json.RawMessage) bool {
	for _, key := range []string{"code", "error_code"} {
		var number json.Number
		if json.Unmarshal(value[key], &number) == nil && number.String() == strconv.Itoa(10085) {
			return true
		}
	}
	for _, key := range []string{"error", "data"} {
		var nested map[string]json.RawMessage
		if json.Unmarshal(value[key], &nested) == nil && nested != nil && chatClientRejected(nested) {
			return true
		}
	}
	return false
}

// Match encoding/json's string escaping while counting fragments, so repeated
// control characters cannot expand the final aggregate beyond its bound.
func chatEncodedTextSize(text string) int {
	size := len(text)
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			size++
		case '<', '>', '&':
			size += 5
		default:
			if text[i] < 0x20 {
				size += 5
			} else if text[i] == 0xe2 && i+2 < len(text) && text[i+1] == 0x80 && (text[i+2] == 0xa8 || text[i+2] == 0xa9) {
				size += 3
				i += 2
			}
		}
	}
	return size
}
