package workbuddy

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// fitChatTools keeps a request-local reversible namespace. Valid client names
// reserve their spelling before other names are assigned, independently of order.
func fitChatTools(body map[string]json.RawMessage, messages []chatMessage) (map[string]string, error) {
	invalid := func() error { return failure("invalid_request", "invalid Chat function tools or tool selection", 400) }
	var selection string
	functionChoice := false
	if raw, exists := body["tool_choice"]; exists {
		if json.Unmarshal(raw, &selection) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var choice struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(raw, &choice) != nil {
				return nil, invalid()
			}
			switch strings.ToLower(strings.TrimSpace(choice.Type)) {
			case "none", "auto", "required":
				selection = strings.ToLower(strings.TrimSpace(choice.Type))
			case "function":
				functionChoice = true
				selection = strings.TrimSpace(choice.Function.Name)
				if selection == "" {
					return nil, invalid()
				}
			default:
				return nil, invalid()
			}
		}
	}
	if functionChoice && (strings.EqualFold(selection, "none") || selection == "auto" || selection == "required") {
		return nil, failure("invalid_request", "the selected function name conflicts with a WorkBuddy tool selection keyword", 400)
	}
	if legacy := body["function_call"]; nonemptyChatValue(legacy) {
		var value string
		if json.Unmarshal(legacy, &value) != nil || !strings.EqualFold(strings.TrimSpace(value), "none") {
			return nil, failure("unsupported_feature", "legacy function calls are not supported", 400)
		}
		selection = "none"
	}
	if strings.EqualFold(strings.TrimSpace(selection), "none") {
		for _, key := range []string{"tools", "functions", "tool_choice", "function_call", "parallel_tool_calls"} {
			delete(body, key)
		}
		return nil, nil
	}
	if nonemptyChatValue(body["functions"]) {
		return nil, failure("unsupported_feature", "legacy function definitions are not supported", 400)
	}
	var tools []map[string]json.RawMessage
	if raw, exists := body["tools"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if json.Unmarshal(raw, &tools) != nil {
			return nil, invalid()
		}
	}
	if len(tools) == 0 {
		if _, exists := body["tool_choice"]; exists {
			if selection != "auto" && selection != "required" {
				return nil, invalid()
			}
			body["tool_choice"] = json.RawMessage(`"` + selection + `"`)
		}
		return nil, nil
	}
	functions := make([]map[string]json.RawMessage, len(tools))
	originals := make([]string, len(tools))
	forward, reverse := make(map[string]string), make(map[string]string)
	taken := make(map[string]bool)
	var unsafe []string
	changed := false
	for i, tool := range tools {
		var kind, name string
		var function map[string]json.RawMessage
		if raw, exists := tool["type"]; exists {
			if json.Unmarshal(raw, &kind) != nil || kind != "function" {
				return nil, invalid()
			}
		} else if tool != nil {
			tool["type"] = json.RawMessage(`"function"`)
			changed = true
		}
		if json.Unmarshal(tool["function"], &function) != nil || function == nil || json.Unmarshal(function["name"], &name) != nil || name == "" {
			return nil, invalid()
		}
		if _, exists := forward[name]; exists {
			return nil, invalid()
		}
		functions[i], originals[i] = function, name
		forward[name] = name
		if safeChatToolName(name) {
			taken[name] = true
		} else {
			unsafe = append(unsafe, name)
		}
	}
	sort.Strings(unsafe)
	for _, name := range unsafe {
		var base strings.Builder
		for _, r := range name {
			if safeChatToolRune(r) {
				base.WriteRune(r)
			} else {
				base.WriteByte('_')
			}
		}
		candidate := base.String()
		for n := 2; taken[candidate]; n++ {
			candidate = base.String() + "_" + strconv.Itoa(n)
		}
		taken[candidate] = true
		forward[name], reverse[candidate] = candidate, name
	}
	for i, function := range functions {
		functionChanged := false
		if safe := forward[originals[i]]; safe != originals[i] {
			encoded, err := json.Marshal(safe)
			if err != nil {
				return nil, invalid()
			}
			function["name"] = encoded
			functionChanged = true
		}
		raw := function["parameters"]
		fitted, err := normalizeToolSchema(raw)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(fitted, raw) {
			function["parameters"] = fitted
			functionChanged = true
		}
		if functionChanged {
			encoded, err := json.Marshal(function)
			if err != nil {
				return nil, invalid()
			}
			tools[i]["function"] = encoded
			changed = true
		}
	}
	if changed {
		encoded, err := json.Marshal(tools)
		if err != nil {
			return nil, invalid()
		}
		body["tools"] = encoded
	}
	if _, exists := body["tool_choice"]; exists {
		if selection != "auto" && selection != "required" {
			safe, exists := forward[selection]
			if !exists {
				return nil, invalid()
			}
			selection = safe
		}
		encoded, err := json.Marshal(selection)
		if err != nil {
			return nil, invalid()
		}
		body["tool_choice"] = encoded
	}
	if len(reverse) == 0 {
		return nil, nil
	}
	for _, message := range messages {
		if raw := message.fields["tool_calls"]; nonemptyChatValue(raw) {
			var calls []map[string]json.RawMessage
			if json.Unmarshal(raw, &calls) != nil {
				return nil, invalid()
			}
			changedCalls := false
			for _, call := range calls {
				var function map[string]json.RawMessage
				var name string
				if json.Unmarshal(call["function"], &function) != nil || function == nil || json.Unmarshal(function["name"], &name) != nil {
					return nil, invalid()
				}
				if safe, exists := forward[name]; exists && safe != name {
					encoded, err := json.Marshal(safe)
					if err != nil {
						return nil, invalid()
					}
					function["name"] = encoded
					encoded, err = json.Marshal(function)
					if err != nil {
						return nil, invalid()
					}
					call["function"] = encoded
					changedCalls = true
				}
			}
			if changedCalls {
				encoded, err := json.Marshal(calls)
				if err != nil {
					return nil, invalid()
				}
				message.fields["tool_calls"] = encoded
			}
		}
		if message.role == "tool" {
			var name string
			if raw, exists := message.fields["name"]; exists {
				if json.Unmarshal(raw, &name) != nil {
					return nil, invalid()
				}
				if safe, exists := forward[name]; exists {
					encoded, err := json.Marshal(safe)
					if err != nil {
						return nil, invalid()
					}
					message.fields["name"] = encoded
				}
			}
		}
	}
	return reverse, nil
}

func safeChatToolName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !safeChatToolRune(r) {
			return false
		}
	}
	return true
}

func safeChatToolRune(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}
