package workbuddy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

func prepareMessages(req executorRequest, stored credential, chosen site, model string, headers map[string][]string, body map[string]json.RawMessage) (preparedChat, error) {
	capabilities := accountChatModel(stored, req.AuthID, model)
	if err := validateMessagesEffort(body, capabilities); err != nil {
		return preparedChat{}, err
	}
	messages, changed, err := readMessagesHistory(body["messages"], capabilities)
	if err != nil {
		return preparedChat{}, err
	}
	if changed {
		rows := make([]map[string]json.RawMessage, 0, len(messages))
		for _, message := range messages {
			rows = append(rows, message.fields)
		}
		body["messages"], err = json.Marshal(rows)
		if err != nil {
			return preparedChat{}, failure("invalid_request", "cannot encode Messages history", 400)
		}
	}
	body["model"], err = json.Marshal(model)
	if err != nil {
		return preparedChat{}, failure("invalid_request", "cannot encode Messages model", 400)
	}
	body["stream"] = json.RawMessage("true")
	payload, err := json.Marshal(body)
	if err != nil {
		return preparedChat{}, failure("invalid_request", "cannot encode Messages request", 400)
	}
	attendChatRequest(headers, req, messages)
	setChatHeaders(headers)
	headers["Accept"] = []string{"text/event-stream"}
	for _, name := range []string{"Anthropic-Version", "Anthropic-Beta"} {
		if value := chatHeader(req.Headers, name); value != "" {
			if !safeValue(value) {
				return preparedChat{}, failure("invalid_request", "invalid Messages protocol header", 400)
			}
			headers[name] = []string{value}
		}
	}
	return preparedChat{request: hostRequest{Method: http.MethodPost, URL: chosen.endpoint + "/v1/messages", Headers: headers, Body: payload, HostCallbackID: req.HostCallbackID}, model: req.Model, messages: true}, nil
}

func validateMessagesEffort(body map[string]json.RawMessage, model *CatalogModel) error {
	invalid := func() error {
		return failure("invalid_request", "invalid native Messages thinking or effort controls", 400)
	}
	for _, field := range []string{"reasoning_effort", "reasoning", "max_completion_tokens", "functions", "function_call"} {
		if nonemptyChatValue(body[field]) {
			return failure("unsupported_feature", "native Messages requires its own request fields", 400)
		}
	}
	if raw := body["output_config"]; nonemptyChatValue(raw) {
		var config map[string]json.RawMessage
		if json.Unmarshal(raw, &config) != nil || config == nil {
			return invalid()
		}
		if raw, exists := config["effort"]; exists && nonemptyChatValue(raw) {
			var effort string
			if json.Unmarshal(raw, &effort) != nil {
				return invalid()
			}
			if model == nil || effort == "none" || !slices.Contains(model.Efforts, effort) {
				return failure("unsupported_feature", "the selected account model does not advertise this Messages effort", 400)
			}
		}
	}
	if raw := body["thinking"]; nonemptyChatValue(raw) {
		var thinking map[string]json.RawMessage
		var kind string
		if json.Unmarshal(raw, &thinking) != nil || thinking == nil || json.Unmarshal(thinking["type"], &kind) != nil || kind == "" {
			return invalid()
		}
		if model == nil || len(model.Efforts) == 0 || kind == "disabled" && !slices.Contains(model.Efforts, "none") {
			return failure("unsupported_feature", "the selected account model does not advertise this thinking control", 400)
		}
	}
	return nil
}

func discardMessagesThinking(kind string, block map[string]json.RawMessage, all bool) bool {
	return kind == "thinking" && (all || !nonemptyChatValue(block["signature"]))
}

func readMessagesHistory(raw json.RawMessage, model *CatalogModel) ([]chatMessage, bool, error) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return nil, false, failure("invalid_request", "Messages history must be a nonempty array of objects", 400)
	}
	messages := make([]chatMessage, 0, len(rows))
	changed := false
	for _, row := range rows {
		var role string
		if json.Unmarshal(row["role"], &role) != nil || role != "user" && role != "assistant" && role != "system" {
			return nil, false, failure("invalid_request", "invalid Messages history role", 400)
		}
		message := chatMessage{fields: row, role: role}
		content, fitted, err := readMessagesContent(row["content"], &message, model, 0)
		if err != nil {
			return nil, false, err
		}
		if fitted {
			row["content"] = content
			changed = true
		}
		messages = append(messages, message)
	}
	return messages, changed, nil
}

func readMessagesContent(raw json.RawMessage, message *chatMessage, model *CatalogModel, depth int) (json.RawMessage, bool, error) {
	invalid := func() error { return failure("invalid_request", "invalid Messages content blocks", 400) }
	if depth > 64 {
		return nil, false, invalid()
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil, false, invalid()
		}
		if message != nil && text != "" {
			message.text = append(message.text, text)
		}
		return raw, false, nil
	}
	var blocks []map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &blocks) != nil {
		return nil, false, invalid()
	}
	kept := blocks[:0]
	changed := false
	for _, block := range blocks {
		var kind string
		if json.Unmarshal(block["type"], &kind) != nil || kind == "" {
			return nil, false, invalid()
		}
		if message != nil && message.role == "assistant" && discardMessagesThinking(kind, block, false) {
			changed = true
			continue
		}
		switch kind {
		case "text":
			var text string
			if json.Unmarshal(block["text"], &text) != nil || !chatHasJSONValue(block["text"]) {
				return nil, false, invalid()
			}
			if message != nil && text != "" {
				message.text = append(message.text, text)
			}
		case "image":
			if model == nil || !model.Images {
				return nil, false, failure("unsupported_feature", "the selected account model does not advertise Messages image input", 400)
			}
			if err := validateMessagesImage(block["source"]); err != nil {
				return nil, false, err
			}
			if message != nil && message.role == "user" {
				digest := sha256.Sum256(block["source"])
				message.images = append(message.images, hex.EncodeToString(digest[:]))
			}
		case "tool_use":
			var id, name string
			var input map[string]json.RawMessage
			if json.Unmarshal(block["id"], &id) != nil || id == "" || json.Unmarshal(block["name"], &name) != nil || name == "" || json.Unmarshal(block["input"], &input) != nil || input == nil {
				return nil, false, invalid()
			}
			if message != nil {
				message.toolUse = true
			}
		case "tool_result":
			var id string
			if json.Unmarshal(block["tool_use_id"], &id) != nil || id == "" {
				return nil, false, invalid()
			}
			if message != nil {
				message.toolResult = true
			}
			if content := block["content"]; chatHasJSONValue(content) {
				if _, _, err := readMessagesContent(content, nil, model, depth+1); err != nil {
					return nil, false, err
				}
			}
		case "thinking":
			var text, signature string
			if json.Unmarshal(block["thinking"], &text) != nil || json.Unmarshal(block["signature"], &signature) != nil || signature == "" {
				return nil, false, invalid()
			}
		}
		kept = append(kept, block)
	}
	if !changed {
		return raw, false, nil
	}
	if len(kept) == 0 {
		return json.RawMessage(`[{"type":"text","text":""}]`), true, nil
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return nil, false, invalid()
	}
	return encoded, true, nil
}

func validateMessagesImage(raw json.RawMessage) error {
	var source map[string]json.RawMessage
	var kind string
	invalid := func() error { return failure("invalid_request", "invalid Messages image source", 400) }
	if json.Unmarshal(raw, &source) != nil || source == nil || json.Unmarshal(source["type"], &kind) != nil {
		return invalid()
	}
	fields := []string{"url"}
	if kind == "base64" {
		fields = []string{"media_type", "data"}
	} else if kind != "url" {
		return failure("unsupported_feature", "Messages images require URL or base64 sources", 400)
	}
	for _, field := range fields {
		var text string
		if json.Unmarshal(source[field], &text) != nil || text == "" {
			return invalid()
		}
	}
	return nil
}

func dropMessagesThinking(raw []byte, all bool) ([]byte, bool, error) {
	var body map[string]json.RawMessage
	var rows []map[string]json.RawMessage
	invalid := func() error {
		return failure("invalid_request", "cannot decode Messages signature recovery request", 400)
	}
	if json.Unmarshal(raw, &body) != nil || body == nil || json.Unmarshal(body["messages"], &rows) != nil {
		return nil, false, invalid()
	}
	changed := false
	for _, row := range rows {
		var role string
		if json.Unmarshal(row["role"], &role) != nil {
			return nil, false, invalid()
		}
		content := bytes.TrimSpace(row["content"])
		if role != "assistant" || len(content) == 0 || content[0] != '[' {
			continue
		}
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(content, &blocks) != nil {
			return nil, false, invalid()
		}
		kept := blocks[:0]
		removed := false
		for _, block := range blocks {
			var kind string
			if json.Unmarshal(block["type"], &kind) != nil {
				return nil, false, invalid()
			}
			if discardMessagesThinking(kind, block, all) {
				removed = true
				continue
			}
			kept = append(kept, block)
		}
		if !removed {
			continue
		}
		changed = true
		if len(kept) == 0 {
			row["content"] = json.RawMessage(`[{"type":"text","text":""}]`)
			continue
		}
		encoded, err := json.Marshal(kept)
		if err != nil {
			return nil, false, invalid()
		}
		row["content"] = encoded
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return nil, false, invalid()
	}
	body["messages"] = encoded
	encoded, err = json.Marshal(body)
	if err != nil {
		return nil, false, invalid()
	}
	return encoded, true, nil
}

var rejectedMessagesSignature = regexp.MustCompile("(?i)thinking_signature_invalid|invalid\\s+`?signature`?\\s+in\\s+`?thinking|block is bound to a different conversation")

func messagesSignatureRejected(body []byte) bool {
	if rejectedMessagesSignature.Match(body) {
		return true
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	return messagesSignatureCode(value, 0)
}

func messagesSignatureCode(value map[string]json.RawMessage, depth int) bool {
	if depth > 8 {
		return false
	}
	for _, field := range []string{"code", "internal_code"} {
		var code json.Number
		if json.Unmarshal(value[field], &code) == nil && code.String() == "11159" {
			return true
		}
	}
	for _, field := range []string{"error", "data"} {
		var child map[string]json.RawMessage
		if json.Unmarshal(value[field], &child) == nil && child != nil && messagesSignatureCode(child, depth+1) {
			return true
		}
		var text string
		if json.Unmarshal(value[field], &text) == nil && strings.HasPrefix(strings.TrimSpace(text), "11159:") {
			return true
		}
	}
	return false
}
