package workbuddy

import (
	"encoding/json"
	"sort"
	"strings"
)

type chatToolCall struct {
	id        string
	name      strings.Builder
	kind      string
	arguments strings.Builder
}

func (choice *chatChoice) addToolDeltas(raw json.RawMessage, names map[string]string, aggregate bool, size *int) (json.RawMessage, error) {
	malformed := func() error {
		return failure("upstream_stream_malformed", "WorkBuddy returned invalid tool call fragments", 502)
	}
	var deltas []map[string]json.RawMessage
	if json.Unmarshal(raw, &deltas) != nil {
		return nil, malformed()
	}
	if len(deltas) == 0 {
		return raw, nil
	}
	if choice.finished {
		return nil, malformed()
	}
	if choice.tools == nil {
		choice.tools = make(map[int]*chatToolCall)
	}
	seen := make(map[int]bool, len(deltas))
	changed := false
	for _, delta := range deltas {
		var index int
		if value, exists := delta["index"]; !exists || !chatHasJSONValue(value) || json.Unmarshal(value, &index) != nil || index < 0 || seen[index] {
			return nil, malformed()
		}
		seen[index] = true
		call := choice.tools[index]
		if call == nil {
			call = &chatToolCall{}
			choice.tools[index] = call
			*size += 128
		}
		for _, field := range []struct {
			key    string
			target *string
		}{{"id", &call.id}, {"type", &call.kind}} {
			if value, exists := delta[field.key]; exists {
				if !chatHasJSONValue(value) {
					continue
				}
				var text string
				if json.Unmarshal(value, &text) != nil {
					return nil, malformed()
				}
				if text == "" {
					continue
				}
				if *field.target != "" && *field.target != text {
					return nil, malformed()
				}
				if *field.target == "" {
					*size += chatEncodedTextSize(text)
					*field.target = text
				}
			}
		}
		if call.kind != "" && call.kind != "function" {
			return nil, malformed()
		}
		if value, exists := delta["function"]; exists && chatHasJSONValue(value) {
			var fn map[string]json.RawMessage
			functionChanged := false
			if json.Unmarshal(value, &fn) != nil || fn == nil {
				return nil, malformed()
			}
			if value, exists := fn["name"]; exists {
				var name string
				if json.Unmarshal(value, &name) != nil {
					return nil, malformed()
				}
				if name != "" {
					*size += chatEncodedTextSize(name)
					call.name.WriteString(name)
				}
				if len(names) > 0 {
					delete(fn, "name")
					changed, functionChanged = true, true
				}
			}
			if value, exists := fn["arguments"]; exists && chatHasJSONValue(value) {
				var text string
				if json.Unmarshal(value, &text) != nil {
					return nil, malformed()
				}
				if aggregate {
					*size += chatEncodedTextSize(text)
					if *size > chatAggregateLimit {
						return nil, failure("upstream_stream_too_large", "WorkBuddy tool arguments exceeded the response limit", 502)
					}
					call.arguments.WriteString(text)
				}
			}
			if functionChanged {
				encoded, err := json.Marshal(fn)
				if err != nil {
					return nil, malformed()
				}
				delta["function"] = encoded
			}
		}
		if *size > chatAggregateLimit {
			return nil, failure("upstream_stream_too_large", "WorkBuddy tool calls exceeded the response limit", 502)
		}
	}
	if !changed {
		return raw, nil
	}
	encoded, err := json.Marshal(deltas)
	if err != nil {
		return nil, malformed()
	}
	return encoded, nil
}

func (choice *chatChoice) completeTools() bool {
	if len(choice.tools) == 0 {
		return true
	}
	seen := make(map[string]bool, len(choice.tools))
	for _, call := range choice.tools {
		if call.id == "" || call.name.Len() == 0 || seen[call.id] {
			return false
		}
		seen[call.id] = true
	}
	return true
}

func (choice *chatChoice) aggregatedTools(names map[string]string) []map[string]any {
	indexes := make([]int, 0, len(choice.tools))
	for index := range choice.tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	calls := make([]map[string]any, 0, len(indexes))
	for _, index := range indexes {
		call := choice.tools[index]
		name := call.name.String()
		if original, exists := names[name]; exists {
			name = original
		}
		calls = append(calls, map[string]any{"id": call.id, "type": "function", "function": map[string]string{"name": name, "arguments": call.arguments.String()}})
	}
	return calls
}

// Names may themselves be fragmented. Publish the complete client spelling at
// the finish boundary; arguments keep flowing while the spelling is collected.
func (choice *chatChoice) finishToolNames(delta map[string]json.RawMessage, names map[string]string) error {
	if len(names) == 0 || len(choice.tools) == 0 {
		return nil
	}
	malformed := func() error {
		return failure("upstream_stream_malformed", "WorkBuddy returned invalid tool call fragments", 502)
	}
	var calls []map[string]json.RawMessage
	if raw := delta["tool_calls"]; chatHasJSONValue(raw) && json.Unmarshal(raw, &calls) != nil {
		return malformed()
	}
	byIndex := make(map[int]map[string]json.RawMessage, len(choice.tools))
	for _, call := range calls {
		var index int
		if json.Unmarshal(call["index"], &index) != nil {
			return malformed()
		}
		byIndex[index] = call
	}
	indexes := make([]int, 0, len(choice.tools))
	for index := range choice.tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := byIndex[index]
		if call == nil {
			encoded, err := json.Marshal(index)
			if err != nil {
				return malformed()
			}
			call = map[string]json.RawMessage{"index": encoded}
			calls = append(calls, call)
		}
		function := make(map[string]json.RawMessage)
		if raw := call["function"]; chatHasJSONValue(raw) && json.Unmarshal(raw, &function) != nil {
			return malformed()
		}
		name := choice.tools[index].name.String()
		if original, exists := names[name]; exists {
			name = original
		}
		encoded, err := json.Marshal(name)
		if err != nil {
			return malformed()
		}
		function["name"] = encoded
		encoded, err = json.Marshal(function)
		if err != nil {
			return malformed()
		}
		call["function"] = encoded
	}
	encoded, err := json.Marshal(calls)
	if err != nil {
		return malformed()
	}
	delta["tool_calls"] = encoded
	return nil
}
