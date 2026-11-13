package workbuddy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// The compatibility rules follow the fixed _ref/index.mjs:842-900,
// 1016-1109, 1531-1588 and 3761-3859. Unknown JSON fields stay RawMessage.
const compatibilityDefaultSystem = "You are a helpful assistant."
const compatibilityDepth = 64

var compatibilityOpener = regexp.MustCompile(`(?i)(\bYou are (?:an?|the)\s[^\n.,]{0,60}?\b(?:agent|assistant))\s+(powered by|running on|running in|built on|based on)\b`)

var compatibilityFlagged = []struct {
	pattern *regexp.Regexp
	to      string
}{
	{regexp.MustCompile(`(?i)x-anthropic-billing-header:[^\n]*\n?`), ""},
	{regexp.MustCompile(`(?i)You are Claude Code, Anthropic['’]s official CLI for Claude`), "You are Claude Code, Anthropic's CLI for Claude"},
	{regexp.MustCompile(`(?i)Main branch \(you will usually use this for PRs\)`), "Main branch (usually the base for PRs)"},
	{regexp.MustCompile(`(?i)You are an AI agent powered by DeepSeek Harness\.?`), "You are an AI agent, powered by DeepSeek Harness."},
	{regexp.MustCompile(`(?i)Web GUI whose URL the runtime context names\.\s+When the user refers`), "Web GUI whose URL the runtime context names. The interface, when the user refers"},
}

var compatibilityCodex = regexp.MustCompile(`(?i)Codex CLI is an open source project led by OpenAI\.`)
var compatibilityDshLines = []struct {
	pattern *regexp.Regexp
	to      string
}{
	{regexp.MustCompile(`You are an AI agent,? powered by DeepSeek Harness\.?`), "You are an AI agent."},
	{regexp.MustCompile(`DeepSeek Harness (Web GUI)`), "$1"},
	{regexp.MustCompile(`DeepSeek Harness (implementation checkout)`), "agent $1"},
	{regexp.MustCompile(`\bDSH itself\b`), "the agent itself"},
}
var compatibilityDshNames = regexp.MustCompile(`DeepSeek Harness|DSH`)

func compatibilityError(message string) error {
	return failure("invalid_request", message, 400)
}

func compatibilityMessages(body map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	raw, ok := body["messages"]
	if !ok {
		return nil, nil
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, compatibilityError("compatibility messages must be an array of objects")
	}
	for _, message := range messages {
		if message == nil {
			return nil, compatibilityError("compatibility messages must be objects")
		}
	}
	return messages, nil
}

func compatibilityString(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	return text
}

func compatibilitySet(object map[string]json.RawMessage, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return compatibilityError("cannot encode compatibility " + key)
	}
	object[key] = raw
	return nil
}

// compatibilityText rewrites only the string or the immediate text of a
// content block. Harness rules exclude explicitly non-text blocks.
func compatibilityText(raw json.RawMessage, rewrite func(string) string, textOnly, dropEmpty bool) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false, nil
	}
	if trimmed[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil, false, compatibilityError("invalid compatibility text")
		}
		next := rewrite(text)
		if next == text {
			return raw, false, nil
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return nil, false, compatibilityError("cannot encode compatibility text")
		}
		return encoded, true, nil
	}
	if trimmed[0] != '[' {
		return raw, false, nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return nil, false, compatibilityError("invalid compatibility content blocks")
	}
	out := make([]json.RawMessage, 0, len(parts))
	changed := false
	for _, part := range parts {
		var fields map[string]json.RawMessage
		if json.Unmarshal(part, &fields) != nil || fields == nil {
			out = append(out, part)
			continue
		}
		var text string
		if json.Unmarshal(fields["text"], &text) != nil || bytes.Equal(bytes.TrimSpace(fields["text"]), []byte("null")) {
			out = append(out, part)
			continue
		}
		if kind, exists := fields["type"]; textOnly && exists && compatibilityString(kind) != "text" {
			out = append(out, part)
			continue
		}
		next := rewrite(text)
		if next == text {
			out = append(out, part)
			continue
		}
		changed = true
		if dropEmpty && strings.TrimSpace(next) == "" {
			continue
		}
		if err := compatibilitySet(fields, "text", next); err != nil {
			return nil, false, err
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, false, compatibilityError("cannot encode compatibility content block")
		}
		out = append(out, encoded)
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, false, compatibilityError("cannot encode compatibility content blocks")
	}
	return encoded, true, nil
}

func commaChatBody(body map[string]json.RawMessage) (changed bool, systemChanged bool, err error) {
	messages, err := compatibilityMessages(body)
	if err != nil {
		return false, false, err
	}
	for _, message := range messages {
		role := compatibilityString(message["role"])
		if role != "system" && role != "assistant" {
			continue
		}
		next, mine, err := compatibilityText(message["content"], func(text string) string {
			return compatibilityOpener.ReplaceAllString(text, "$1, $2")
		}, false, false)
		if err != nil {
			return false, false, err
		}
		if mine {
			message["content"] = next
			changed = true
			systemChanged = systemChanged || role == "system"
		}
	}
	if changed {
		err = compatibilitySet(body, "messages", messages)
	}
	return changed, systemChanged, err
}

func compatibilityUnflag(text string) string {
	// RE2 has no negative lookbehind: retain Codex's already-prefixed line.
	matches := compatibilityCodex.FindAllStringIndex(text, -1)
	if len(matches) > 0 {
		var out strings.Builder
		last := 0
		for _, match := range matches {
			out.WriteString(text[last:match[0]])
			if match[0] < 4 || !strings.EqualFold(text[match[0]-4:match[0]], "the ") {
				out.WriteString("The Codex CLI is an open source project led by OpenAI.")
			} else {
				out.WriteString(text[match[0]:match[1]])
			}
			last = match[1]
		}
		out.WriteString(text[last:])
		text = out.String()
	}
	for _, rule := range compatibilityFlagged {
		text = rule.pattern.ReplaceAllString(text, rule.to)
	}
	return text
}

func compatibilityDsh(text string, said bool) string {
	for _, rule := range compatibilityDshLines {
		text = rule.pattern.ReplaceAllString(text, rule.to)
	}
	if said {
		return text
	}
	matches := compatibilityDshNames.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	last := 0
	for _, match := range matches {
		// The optional article can match only when its own left boundary
		// passes. Otherwise JavaScript retries at the harness name itself.
		if text[match[0]:match[1]] == "DeepSeek Harness" && match[0] >= 4 {
			start := match[0] - 4
			article := text[start:match[0]]
			if (article == "The " || article == "the ") && (start == 0 || !compatibilityPathByte(text[start-1], true)) {
				match[0] = start
			}
		}
		before := match[0] > 0 && compatibilityPathByte(text[match[0]-1], true)
		after := match[1] < len(text) && compatibilityPathByte(text[match[1]], false)
		if match[1]+1 < len(text) && text[match[1]] == '.' && compatibilityWordByte(text[match[1]+1]) {
			after = true
		}
		out.WriteString(text[last:match[0]])
		if before || after {
			out.WriteString(text[match[0]:match[1]])
		} else if strings.HasPrefix(text[match[0]:match[1]], "The ") {
			out.WriteString("The agent harness")
		} else {
			out.WriteString("the agent harness")
		}
		last = match[1]
	}
	out.WriteString(text[last:])
	return out.String()
}

func compatibilityWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

func compatibilityPathByte(c byte, before bool) bool {
	return compatibilityWordByte(c) || c == '/' || c == '\\' || c == '@' || c == '-' || before && (c == '.' || c == '~' || c == ':')
}

func normalizeHarnessBody(body map[string]json.RawMessage, nativeMessages bool) error {
	messages, err := compatibilityMessages(body)
	if err != nil {
		return err
	}
	out := make([]map[string]json.RawMessage, 0, len(messages))
	changed := false
	for _, message := range messages {
		role := compatibilityString(message["role"])
		content := message["content"]
		mine := false
		if !nativeMessages {
			content, mine, err = compatibilityText(content, compatibilityUnflag, false, true)
			if err != nil {
				return err
			}
			if mine && role == "system" && compatibilityEmptyContent(content) {
				changed = true
				continue
			}
		}
		if role != "tool" {
			var hidden bool
			content, hidden, err = compatibilityText(content, func(text string) string {
				return compatibilityDsh(text, role != "system" && role != "developer")
			}, true, false)
			if err != nil {
				return err
			}
			mine = mine || hidden
		}
		if mine {
			message["content"] = content
			changed = true
		}
		out = append(out, message)
	}
	if changed {
		if err := compatibilitySet(body, "messages", out); err != nil {
			return err
		}
	}
	if raw, ok := body["system"]; ok {
		next, changed, err := compatibilityText(raw, func(text string) string { return compatibilityDsh(text, false) }, true, false)
		if err != nil {
			return err
		}
		if changed {
			body["system"] = next
		}
	}
	if raw, ok := body["tools"]; ok {
		next, changed, err := compatibilityDescriptions(raw, 0)
		if err != nil {
			return err
		}
		if changed {
			body["tools"] = next
		}
	}
	return nil
}

func compatibilityEmptyContent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var parts []json.RawMessage
		return json.Unmarshal(raw, &parts) == nil && len(parts) == 0
	}
	return strings.TrimSpace(compatibilityString(raw)) == ""
}

func compatibilityDescriptions(raw json.RawMessage, depth int) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, compatibilityError("invalid compatibility tool JSON")
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return raw, false, nil
	}
	if depth >= compatibilityDepth {
		return raw, false, nil
	}
	changed := false
	var value any
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil, false, compatibilityError("invalid compatibility tools array")
		}
		for i, item := range items {
			next, mine, err := compatibilityDescriptions(item, depth+1)
			if err != nil {
				return nil, false, err
			}
			if mine {
				items[i] = next
				changed = true
			}
		}
		value = items
	} else {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil, false, compatibilityError("invalid compatibility tool object")
		}
		for key, item := range fields {
			if key == "description" && len(bytes.TrimSpace(item)) > 0 && bytes.TrimSpace(item)[0] == '"' {
				text := compatibilityString(item)
				next := compatibilityDsh(text, false)
				if next != text {
					if err := compatibilitySet(fields, key, next); err != nil {
						return nil, false, err
					}
					changed = true
				}
				continue
			}
			next, mine, err := compatibilityDescriptions(item, depth+1)
			if err != nil {
				return nil, false, err
			}
			if mine {
				fields[key] = next
				changed = true
			}
		}
		value = fields
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false, compatibilityError("cannot encode compatibility tools")
	}
	return encoded, true, nil
}

func replyChatAfterTools(body map[string]json.RawMessage) (bool, error) {
	messages, err := compatibilityMessages(body)
	if err != nil {
		return false, err
	}
	i := len(messages) - 1
	for i >= 0 && compatibilityString(messages[i]["role"]) == "user" {
		i--
	}
	if i < 0 || i == len(messages)-1 || compatibilityString(messages[i]["role"]) != "tool" {
		return false, nil
	}
	messages = append(messages, nil)
	copy(messages[i+2:], messages[i+1:])
	messages[i+1] = map[string]json.RawMessage{"role": json.RawMessage(`"assistant"`), "content": json.RawMessage(`""`)}
	return true, compatibilitySet(body, "messages", messages)
}

func compatibilityContentText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return compatibilityString(raw)
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		if text := compatibilityString(part["text"]); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// A default-only prompt needs no remembered policy. Canonical RawMessage
// decoding includes blocks and metadata in the hash without retaining prompts.
func chatSystemKey(body map[string]json.RawMessage) string {
	messages, err := compatibilityMessages(body)
	if err != nil {
		return ""
	}
	var systems []map[string]json.RawMessage
	for _, message := range messages {
		if compatibilityString(message["role"]) == "system" {
			systems = append(systems, message)
		}
	}
	if len(systems) == 0 || len(systems) == 1 && len(systems[0]) == 2 && strings.TrimSpace(compatibilityString(systems[0]["content"])) == compatibilityDefaultSystem {
		return ""
	}
	raw, err := json.Marshal(systems)
	if err != nil {
		return ""
	}
	// Decoder.UseNumber preserves numeric extension fields while canonicalizing
	// nested object key order, so insignificant JSON whitespace cannot split keys.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var canonical any
	if decoder.Decode(&canonical) != nil {
		return ""
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func moveChatSystem(body map[string]json.RawMessage) (bool, error) {
	messages, err := compatibilityMessages(body)
	if err != nil {
		return false, err
	}
	lead := 0
	var texts []string
	preserve := false
	for lead < len(messages) && compatibilityString(messages[lead]["role"]) == "system" {
		message := messages[lead]
		texts = append(texts, compatibilityContentText(message["content"]))
		content := bytes.TrimSpace(message["content"])
		preserve = preserve || len(message) != 2 || len(content) == 0 || content[0] != '"'
		lead++
	}
	said := strings.TrimSpace(strings.Join(texts, "\n\n"))
	moved := said != "" && said != compatibilityDefaultSystem
	rest := messages[lead:]
	later := false
	for _, message := range rest {
		if compatibilityString(message["role"]) == "system" {
			message["role"] = json.RawMessage(`"user"`)
			later = true
		}
	}
	if !moved && !later {
		return false, nil
	}
	if preserve {
		// Flattening leading blocks loses cache_control and future fields. Keep
		// each message and block, adding instructions as separate text blocks.
		prefix := make([]map[string]json.RawMessage, 0, lead+len(rest))
		for _, message := range messages[:lead] {
			content := bytes.TrimSpace(message["content"])
			parts := []json.RawMessage{json.RawMessage(`{"type":"text","text":"<instructions>\n"}`)}
			if len(content) > 0 && content[0] == '[' {
				var own []json.RawMessage
				if json.Unmarshal(content, &own) != nil {
					return false, compatibilityError("invalid system content blocks")
				}
				parts = append(parts, own...)
			} else if len(content) > 0 && content[0] == '"' {
				part := map[string]json.RawMessage{"type": json.RawMessage(`"text"`), "text": message["content"]}
				encoded, err := json.Marshal(part)
				if err != nil {
					return false, compatibilityError("cannot encode system instructions")
				}
				parts = append(parts, encoded)
			} else {
				return false, compatibilityError("system instructions must contain text or content blocks")
			}
			parts = append(parts, json.RawMessage(`{"type":"text","text":"\n</instructions>"}`))
			message["role"] = json.RawMessage(`"user"`)
			if err := compatibilitySet(message, "content", parts); err != nil {
				return false, err
			}
			prefix = append(prefix, message)
		}
		rest = append(prefix, rest...)
	} else if moved {
		text := "<instructions>\n" + said + "\n</instructions>"
		u := -1
		for i, message := range rest {
			if compatibilityString(message["role"]) == "user" {
				u = i
				break
			}
		}
		if u < 0 {
			message := map[string]json.RawMessage{"role": json.RawMessage(`"user"`)}
			if err := compatibilitySet(message, "content", text); err != nil {
				return false, err
			}
			rest = append([]map[string]json.RawMessage{message}, rest...)
		} else {
			content := bytes.TrimSpace(rest[u]["content"])
			if len(content) > 0 && content[0] == '[' {
				var parts []json.RawMessage
				if json.Unmarshal(content, &parts) != nil {
					return false, compatibilityError("invalid user content blocks")
				}
				part := map[string]string{"type": "text", "text": text}
				encoded, err := json.Marshal(part)
				if err != nil {
					return false, compatibilityError("cannot encode system instructions")
				}
				if err := compatibilitySet(rest[u], "content", append([]json.RawMessage{encoded}, parts...)); err != nil {
					return false, err
				}
			} else {
				if len(content) > 0 && content[0] == '"' {
					text += "\n\n" + compatibilityString(content)
				}
				if err := compatibilitySet(rest[u], "content", text); err != nil {
					return false, err
				}
			}
		}
	}
	all := append([]map[string]json.RawMessage{{"role": json.RawMessage(`"system"`), "content": json.RawMessage(`"` + compatibilityDefaultSystem + `"`)}}, rest...)
	return true, compatibilitySet(body, "messages", all)
}

func addChatGrowth(body map[string]json.RawMessage, conversationID, modelName string) error {
	if conversationID == "" {
		return nil
	}
	messages, err := compatibilityMessages(body)
	if err != nil {
		return err
	}
	if messages == nil {
		return nil
	}
	vars := make(map[string]json.RawMessage)
	if raw := bytes.TrimSpace(body["extra_vars"]); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if json.Unmarshal(raw, &vars) != nil || vars == nil {
			return compatibilityError("extra_vars must be an object")
		}
	}
	var events []json.RawMessage
	if text := compatibilityString(vars["growthEvent"]); strings.TrimSpace(text) != "" {
		var event json.RawMessage
		if json.Unmarshal([]byte(text), &event) == nil {
			if bytes.TrimSpace(event)[0] == '[' {
				if json.Unmarshal(event, &events) != nil {
					return compatibilityError("invalid growth events array")
				}
			} else {
				events = []json.RawMessage{event}
			}
		}
	}
	found := false
	for _, event := range events {
		var fields map[string]json.RawMessage
		if json.Unmarshal(event, &fields) == nil && compatibilityString(fields["eventCode"]) == "chat_request_send" && compatibilityString(fields["id"]) == conversationID {
			found = true
		}
	}
	if !found {
		length, err := compatibilityInputLength(messages)
		if err != nil {
			return err
		}
		model := compatibilityString(body["model"])
		if modelName == "" {
			modelName = model
		}
		event := struct {
			EventCode string         `json:"eventCode"`
			ID        string         `json:"id"`
			Extra     map[string]any `json:"extra"`
		}{"chat_request_send", conversationID, map[string]any{"inputLength": length, "requestModelId": model, "requestModelName": modelName, "mode": "craft", "command": ""}}
		encoded, err := json.Marshal(event)
		if err != nil {
			return compatibilityError("cannot encode growth event")
		}
		events = append(events, encoded)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		return compatibilityError("cannot encode growth events")
	}
	if err := compatibilitySet(vars, "growthEvent", string(encoded)); err != nil {
		return err
	}
	if raw, ok := vars["initiator"]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		vars["initiator"] = json.RawMessage(`"user"`)
	}
	return compatibilitySet(body, "extra_vars", vars)
}

func compatibilityInputLength(messages []map[string]json.RawMessage) (int, error) {
	for i := range slices.Backward(messages) {
		if compatibilityString(messages[i]["role"]) != "user" || compatibilityHasBlock(messages[i], "tool_result") {
			continue
		}
		previous := i - 1
		for previous >= 0 && compatibilityString(messages[previous]["role"]) == "system" {
			previous--
		}
		if previous >= 0 {
			role := compatibilityString(messages[previous]["role"])
			if role == "tool" || role == "user" && compatibilityHasBlock(messages[previous], "tool_result") || role == "assistant" && (hasChatToolCalls(messages[previous]) || compatibilityHasBlock(messages[previous], "tool_use")) {
				continue
			}
		}
		content := bytes.TrimSpace(messages[i]["content"])
		if len(content) == 0 {
			return 0, nil
		}
		if content[0] == '"' {
			return compatibilityUTF16Length(compatibilityString(content)), nil
		}
		if content[0] != '[' {
			return 0, nil
		}
		var parts []map[string]json.RawMessage
		if json.Unmarshal(content, &parts) != nil {
			return 0, compatibilityError("invalid growth input content blocks")
		}
		length := 0
		for _, part := range parts {
			kind, exists := part["type"]
			if !exists || bytes.Equal(bytes.TrimSpace(kind), []byte("null")) || compatibilityString(kind) == "text" {
				length += compatibilityUTF16Length(compatibilityString(part["text"]))
			}
		}
		return length, nil
	}
	return 0, nil
}

func compatibilityHasBlock(message map[string]json.RawMessage, kind string) bool {
	var parts []map[string]json.RawMessage
	if json.Unmarshal(message["content"], &parts) != nil {
		return false
	}
	for _, part := range parts {
		if compatibilityString(part["type"]) == kind {
			return true
		}
	}
	return false
}

func compatibilityUTF16Length(text string) int {
	length := 0
	for _, r := range text {
		length++
		if r > 0xffff {
			length++
		}
	}
	return length
}
