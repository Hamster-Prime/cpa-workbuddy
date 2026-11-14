package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/translator/builtin"
)

// chatTranslation owns request-local protocol context. Format describes the
// client's response; SourceFormat describes Payload. The selected account's site
// and model determine the upstream protocol.
type chatTranslation struct {
	from, to translator.Format
	original []byte
	request  []byte
}

func supportedChatFormat(format string) bool {
	switch format {
	case "openai", "openai-response", "claude", "gemini":
		return true
	default:
		return false
	}
}

func translateChatRequest(req executorRequest, native, model string, capabilities *CatalogModel) (*chatTranslation, []byte, error) {
	source := req.SourceFormat
	if source == "" {
		source = req.Format
	}
	if !supportedChatFormat(req.Format) || !supportedChatFormat(source) {
		return nil, nil, failure("unsupported_format", "WorkBuddy supports openai, openai-response, claude, and gemini formats", 400)
	}
	if len(req.Payload) > maxChatPayload || len(req.OriginalRequest) > maxChatPayload {
		return nil, nil, failure("request_too_large", "chat payload exceeds the 32 MiB limit", 413)
	}
	payload := req.Payload
	registry := builtin.Registry()
	if source != native {
		var body map[string]json.RawMessage
		if err := decode(req.Payload, &body); err != nil {
			return nil, nil, err
		}
		if body == nil {
			return nil, nil, failure("invalid_request", "chat payload must be a JSON object", 400)
		}
		var err error
		payload, err = prepareTranslatedControls(source, payload, body, capabilities)
		if err != nil {
			return nil, nil, err
		}
		from, to := translator.Format(source), translator.Format(native)
		if !registry.HasRequestTransformer(from, to) {
			return nil, nil, failure("unsupported_format", "WorkBuddy request translation is unavailable", 400)
		}
		translated := registry.TranslateRequestEnvelope(context.Background(), from, to, translator.RequestEnvelope{Format: from, Model: model, Stream: true, Body: payload})
		if translated.Err != nil {
			return nil, nil, failure("invalid_request", "the client request cannot be represented by the selected WorkBuddy protocol", 400)
		}
		payload = translated.Body
		if len(payload) > maxChatPayload {
			return nil, nil, failure("request_too_large", "translated chat payload exceeds the 32 MiB limit", 413)
		}
	}
	if req.Format == native {
		return nil, payload, nil
	}
	from, to := translator.Format(native), translator.Format(req.Format)
	// The registry indexes responses by the corresponding request route (to,
	// from); TranslateStream/TranslateNonStream accept response direction.
	if !registry.HasStreamResponseTransformer(to, from) || !registry.HasNonStreamResponseTransformer(to, from) {
		return nil, nil, failure("unsupported_format", "WorkBuddy response translation is unavailable", 400)
	}
	original := req.OriginalRequest
	if len(original) == 0 {
		original = req.Payload
	}
	return &chatTranslation{from: from, to: to, original: original, request: payload}, payload, nil
}

func (prepared *preparedChat) consume(read func() ([]byte, bool, error), emit func([]byte) error) ([]byte, error) {
	consumeNative := func(emit func([]byte) error) ([]byte, error) {
		if prepared.messages {
			return consumeMessagesStream(read, prepared.model, emit)
		}
		return consumeChatStream(read, prepared.model, prepared.toolNames, emit)
	}
	t := prepared.translation
	if t == nil || t.from == t.to {
		return consumeNative(emit)
	}
	registry := builtin.Registry()
	ctx := context.Background()
	var state any
	var messages messagesTranslation
	var chat chatResponseTranslation
	if emit == nil {
		var native []byte
		var err error
		if t.from == translator.FormatClaude && t.to == translator.FormatGemini {
			// This official route consumes SSE even in non-streaming mode. Retain
			// validated records, bounded independently from transport chunk size.
			var events bytes.Buffer
			_, err = consumeNative(func(frame []byte) error {
				return translateNativeRecord(frame, &messages, func(data []byte) error {
					if events.Len()+len(data)+8 > chatAggregateLimit {
						return failure("upstream_stream_too_large", "WorkBuddy translated response exceeded the response limit", 502)
					}
					events.WriteString("data: ")
					events.Write(data)
					events.WriteString("\n\n")
					return nil
				})
			})
			native = events.Bytes()
		} else {
			native, err = consumeNative(nil)
			if err == nil && t.from == translator.FormatClaude {
				native, messages.usage, err = normalizeMessagesUsage(native)
			}
			if err == nil && t.from == translator.FormatOpenAI {
				native, err = chat.normalizeNative(native, t.to == translator.FormatGemini)
			}
		}
		if err != nil {
			return nil, err
		}
		out := registry.TranslateNonStream(ctx, t.from, t.to, prepared.model, t.original, t.request, native, &state)
		if err := responseTranslationError(state); err != nil {
			return nil, err
		}
		if len(out) == 0 || !json.Valid(out) {
			return nil, failure("upstream_translation_failed", "WorkBuddy response could not be translated", 502)
		}
		if t.from == translator.FormatClaude && t.to == translator.FormatGemini {
			out, err = messages.geminiResult(out)
			if err != nil {
				return nil, err
			}
		}
		if t.from == translator.FormatClaude && t.to == translator.FormatOpenAIResponse {
			out, err = messages.responsesResult(out)
			if err != nil {
				return nil, err
			}
		}
		if t.from == translator.FormatOpenAI && t.to == translator.FormatClaude {
			out, err = chat.claudeResult(out, false)
			if err != nil {
				return nil, err
			}
		}
		if len(out) > chatAggregateLimit {
			return nil, failure("upstream_stream_too_large", "WorkBuddy translated response exceeded the response limit", 502)
		}
		return out, nil
	}
	// Claude's official Chat adapter uses the original request's stream flag.
	// The RPC method is authoritative, including clients that omit that flag.
	original, err := streamingTranslationRequest(t.original)
	if err != nil {
		return nil, err
	}
	_, err = consumeNative(func(frame []byte) error {
		var messageState *messagesTranslation
		if t.from == translator.FormatClaude {
			messageState = &messages
		}
		return translateNativeRecord(frame, messageState, func(data []byte) error {
			if t.from == translator.FormatOpenAI {
				data, err = chat.normalizeNative(data, t.to == translator.FormatGemini)
				if err != nil {
					return err
				}
			}
			input := make([]byte, 0, len(data)+6)
			input = append(input, "data: "...)
			input = append(input, data...)
			for _, output := range registry.TranslateStream(ctx, t.from, t.to, prepared.model, original, t.request, input, &state) {
				if len(output) == 0 {
					continue
				}
				if t.from == translator.FormatClaude && t.to == translator.FormatGemini {
					output, err = messages.geminiResult(output)
					if err != nil {
						return err
					}
				}
				if t.from == translator.FormatClaude && t.to == translator.FormatOpenAIResponse {
					output, err = messages.responsesResult(output)
					if err != nil {
						return err
					}
				}
				if t.from == translator.FormatOpenAI && t.to == translator.FormatClaude {
					output, err = chat.claudeResult(output, true)
					if err != nil {
						return err
					}
				}
				if err := emitTranslatedRecord(output, emit); err != nil {
					return err
				}
			}
			return responseTranslationError(state)
		})
	})
	if err != nil {
		return nil, err
	}
	if t.to == translator.FormatOpenAI {
		// The official Claude -> Chat converter returns JSON chunks and leaves
		// the Chat transport terminator to its caller.
		if err := emit([]byte("data: [DONE]\n\n")); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func responseTranslationError(state any) error {
	if failed, ok := state.(interface{ ToolInputError() error }); ok && failed.ToolInputError() != nil {
		return failure("upstream_translation_failed", "WorkBuddy tool input could not be translated", 502)
	}
	return nil
}

func streamingTranslationRequest(original []byte) ([]byte, error) {
	var body map[string]json.RawMessage
	if json.Unmarshal(original, &body) != nil || body == nil {
		return nil, failure("invalid_request", "the original client request must be a JSON object", 400)
	}
	if bytes.Equal(bytes.TrimSpace(body["stream"]), []byte("true")) {
		return original, nil
	}
	body["stream"] = json.RawMessage("true")
	return json.Marshal(body)
}

// Native consumers emit complete SSE records, possibly with event/id/retry
// fields. The SDK expects one data: line per call, never an entire SSE record
// beginning with event:, nor arbitrary HTTP transport chunks.
func translateNativeRecord(frame []byte, messages *messagesTranslation, accept func([]byte) error) error {
	var decoder eventStreamDecoder
	_, err := decoder.feed(frame, func(event streamEvent) (bool, error) {
		if !event.hasData {
			return false, nil
		}
		if messages != nil {
			return false, messages.record(event.data, accept)
		}
		return false, accept(event.data)
	}, func() error {
		return failure("upstream_stream_too_large", "WorkBuddy response event exceeded the response limit", 502)
	})
	return err
}

func emitTranslatedRecord(output []byte, emit func([]byte) error) error {
	trimmed := bytes.TrimSpace(output)
	if bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:")) {
		if bytes.HasSuffix(output, []byte("\n\n")) {
			return emit(output)
		}
		frame := append([]byte(nil), bytes.TrimRight(output, "\r\n")...)
		frame = append(frame, '\n', '\n')
		return emit(frame)
	}
	if !json.Valid(trimmed) {
		return failure("upstream_translation_failed", "WorkBuddy returned an invalid translated event", 502)
	}
	frame := make([]byte, 0, len(output)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, output...)
	frame = append(frame, '\n', '\n')
	return emit(frame)
}

// Messages reports input usage at message_start and output usage at
// message_delta. Keep both before invoking adapters that inspect only the latter.
type messagesTranslation struct {
	usage map[string]json.RawMessage
	stop  string
}

func canonicalMessagesUsage(usage map[string]json.RawMessage) {
	thinking := usage["reasoning_tokens"]
	if !chatHasJSONValue(thinking) {
		thinking = usage["thinking_tokens"]
	}
	var details map[string]json.RawMessage
	if raw := usage["output_tokens_details"]; chatHasJSONValue(raw) {
		_ = json.Unmarshal(raw, &details)
	}
	if !chatHasJSONValue(thinking) {
		thinking = details["reasoning_tokens"]
	}
	if !chatHasJSONValue(thinking) {
		thinking = details["thinking_tokens"]
	}
	if chatHasJSONValue(thinking) {
		if details == nil {
			details = make(map[string]json.RawMessage)
		}
		usage["thinking_tokens"] = thinking
		usage["reasoning_tokens"] = thinking
		details["thinking_tokens"] = thinking
		usage["output_tokens_details"], _ = json.Marshal(details)
	}
}

func normalizeMessagesUsage(payload []byte) ([]byte, map[string]json.RawMessage, error) {
	var message map[string]json.RawMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, nil, err
	}
	var usage map[string]json.RawMessage
	if raw := message["usage"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &usage); err != nil {
			return nil, nil, err
		}
		canonicalMessagesUsage(usage)
		encoded, err := json.Marshal(usage)
		if err != nil {
			return nil, nil, err
		}
		message["usage"] = encoded
	}
	result, err := json.Marshal(message)
	return result, usage, err
}

func (m *messagesTranslation) record(data []byte, accept func([]byte) error) error {
	var event map[string]json.RawMessage
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	var kind string
	_ = json.Unmarshal(event["type"], &kind)
	if kind == "message_start" || kind == "message_delta" {
		container := event
		if kind == "message_start" {
			container = nil
			_ = json.Unmarshal(event["message"], &container)
		}
		var usage map[string]json.RawMessage
		_ = json.Unmarshal(container["usage"], &usage)
		canonicalMessagesUsage(usage)
		if m.usage == nil {
			m.usage = make(map[string]json.RawMessage)
		}
		maps.Copy(m.usage, usage)
		canonicalMessagesUsage(m.usage)
		container["usage"], _ = json.Marshal(m.usage)
		if kind == "message_start" {
			event["message"], _ = json.Marshal(container)
		} else {
			var delta struct {
				StopReason string `json:"stop_reason"`
			}
			_ = json.Unmarshal(event["delta"], &delta)
			if delta.StopReason != "" {
				m.stop = delta.StopReason
			}
		}
		data, _ = json.Marshal(event)
	}
	if kind == "content_block_start" {
		var block map[string]json.RawMessage
		_ = json.Unmarshal(event["content_block"], &block)
		var blockType string
		_ = json.Unmarshal(block["type"], &blockType)
		field, deltaType := "", ""
		switch blockType {
		case "text":
			field, deltaType = "text", "text_delta"
		case "thinking":
			field, deltaType = "thinking", "thinking_delta"
		case "tool_use":
			field, deltaType = "input", "input_json_delta"
		}
		initial := block[field]
		if field != "" && chatHasJSONValue(initial) && !bytes.Equal(initial, []byte(`""`)) && !bytes.Equal(bytes.TrimSpace(initial), []byte("{}")) {
			block[field] = json.RawMessage(`""`)
			deltaField := field
			if field == "input" {
				block[field] = json.RawMessage("{}")
				deltaField = "partial_json"
				initial, _ = json.Marshal(string(initial))
			}
			event["content_block"], _ = json.Marshal(block)
			start, _ := json.Marshal(event)
			if err := accept(start); err != nil {
				return err
			}
			delta := map[string]json.RawMessage{"type": json.RawMessage(`"content_block_delta"`), "index": event["index"]}
			delta["delta"], _ = json.Marshal(map[string]json.RawMessage{"type": json.RawMessage(`"` + deltaType + `"`), deltaField: initial})
			data, _ = json.Marshal(delta)
		}
	}
	return accept(data)
}

// CPA's Claude -> Gemini adapter drops initial usage and unconditionally emits
// STOP. Restore the validated native counts and stop reason within the plugin.
func (m *messagesTranslation) geminiResult(payload []byte) ([]byte, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	var candidates []map[string]json.RawMessage
	_ = json.Unmarshal(result["candidates"], &candidates)
	if m.stop == "" {
		changed := false
		for _, candidate := range candidates {
			if _, exists := candidate["finishReason"]; exists {
				delete(candidate, "finishReason")
				changed = true
			}
		}
		if !changed {
			return payload, nil
		}
		result["candidates"], _ = json.Marshal(candidates)
		return json.Marshal(result)
	}
	finish := "STOP"
	switch m.stop {
	case "max_tokens", "model_context_window_exceeded":
		finish = "MAX_TOKENS"
	case "refusal", "sensitive":
		finish = "SAFETY"
	}
	for _, candidate := range candidates {
		candidate["finishReason"], _ = json.Marshal(finish)
	}
	result["candidates"], _ = json.Marshal(candidates)
	count := func(key string) int64 {
		var value int64
		_ = json.Unmarshal(m.usage[key], &value)
		return value
	}
	input := count("input_tokens") + count("cache_creation_input_tokens") + count("cache_read_input_tokens")
	output := count("output_tokens")
	usage := map[string]any{"promptTokenCount": input, "candidatesTokenCount": output, "totalTokenCount": input + output}
	if chatHasJSONValue(m.usage["cache_read_input_tokens"]) {
		usage["cachedContentTokenCount"] = count("cache_read_input_tokens")
	}
	if chatHasJSONValue(m.usage["thinking_tokens"]) {
		usage["thoughtsTokenCount"] = count("thinking_tokens")
	}
	result["usageMetadata"], _ = json.Marshal(usage)
	return json.Marshal(result)
}

// Explicit upstream reasoning counts take precedence over the SDK's estimate.
func (m *messagesTranslation) responsesResult(payload []byte) ([]byte, error) {
	count := m.usage["reasoning_tokens"]
	if !chatHasJSONValue(count) {
		return payload, nil
	}
	data := payload
	var prefix []byte
	if !json.Valid(payload) {
		index := bytes.Index(payload, []byte("data:"))
		if index < 0 {
			return payload, nil
		}
		start := index + len("data:")
		for start < len(payload) && payload[start] == ' ' {
			start++
		}
		prefix, data = payload[:start], bytes.TrimSpace(payload[start:])
	}
	var event map[string]json.RawMessage
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	response := event
	if nested := event["response"]; len(nested) != 0 {
		response = nil
		if err := json.Unmarshal(nested, &response); err != nil {
			return nil, err
		}
	}
	if len(response["usage"]) == 0 {
		return payload, nil
	}
	var usage map[string]json.RawMessage
	if err := json.Unmarshal(response["usage"], &usage); err != nil {
		return nil, err
	}
	details := make(map[string]json.RawMessage)
	if raw := usage["output_tokens_details"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &details); err != nil {
			return nil, err
		}
	}
	if details == nil {
		details = make(map[string]json.RawMessage)
	}
	details["reasoning_tokens"] = count
	encoded, err := json.Marshal(details)
	if err != nil {
		return nil, err
	}
	usage["output_tokens_details"] = encoded
	encoded, err = json.Marshal(usage)
	if err != nil {
		return nil, err
	}
	response["usage"] = encoded
	if _, nested := event["response"]; nested {
		encoded, err = json.Marshal(response)
		if err != nil {
			return nil, err
		}
		event["response"] = encoded
	}
	encoded, err = json.Marshal(event)
	if err != nil || prefix == nil {
		return encoded, err
	}
	frame := make([]byte, 0, len(prefix)+len(encoded)+2)
	frame = append(frame, prefix...)
	frame = append(frame, encoded...)
	return append(frame, '\n', '\n'), nil
}

// Chat's Claude adapter does not copy reasoning token counts, and its Gemini
// adapter reads only reasoning_content. Adapt the validated native values before
// and after the SDK call without merging reasoning into visible answer text.
type chatResponseTranslation struct {
	reasoning json.RawMessage
}

func (c *chatResponseTranslation) normalizeNative(payload []byte, gemini bool) ([]byte, error) {
	if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		return payload, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, err
	}
	var usage map[string]json.RawMessage
	if json.Unmarshal(root["usage"], &usage) == nil {
		for _, key := range []string{"completion_tokens_details", "output_tokens_details"} {
			var details map[string]json.RawMessage
			if json.Unmarshal(usage[key], &details) == nil && chatHasJSONValue(details["reasoning_tokens"]) {
				c.reasoning = details["reasoning_tokens"]
				break
			}
		}
	}
	if !gemini {
		return payload, nil
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(root["choices"], &choices); err != nil {
		return nil, err
	}
	changed := false
	for _, choice := range choices {
		key := "delta"
		if _, ok := choice["message"]; ok {
			key = "message"
		}
		var message map[string]json.RawMessage
		if json.Unmarshal(choice[key], &message) != nil || message == nil {
			continue
		}
		if !chatHasJSONValue(message["reasoning"]) && !chatHasJSONValue(message["reasoning_details"]) {
			continue
		}
		var texts []string
		for _, field := range []string{"reasoning_content", "reasoning", "reasoning_details"} {
			texts = append(texts, translatedReasoningTexts(message[field])...)
		}
		message["reasoning_content"], _ = json.Marshal(texts)
		choice[key], _ = json.Marshal(message)
		changed = true
	}
	if !changed {
		return payload, nil
	}
	root["choices"], _ = json.Marshal(choices)
	return json.Marshal(root)
}

func translatedReasoningTexts(raw json.RawMessage) []string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []string{text}
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		var texts []string
		for _, item := range items {
			texts = append(texts, translatedReasoningTexts(item)...)
		}
		return texts
	}
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) == nil {
		for _, key := range []string{"text", "summary", "thinking", "content"} {
			if value, ok := item[key]; ok {
				return translatedReasoningTexts(value)
			}
		}
	}
	return nil
}

func (c *chatResponseTranslation) claudeResult(payload []byte, streaming bool) ([]byte, error) {
	if !chatHasJSONValue(c.reasoning) {
		return payload, nil
	}
	patch := func(data []byte) ([]byte, error) {
		var root map[string]json.RawMessage
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, err
		}
		if !chatHasJSONValue(root["usage"]) {
			return data, nil
		}
		var usage map[string]json.RawMessage
		if err := json.Unmarshal(root["usage"], &usage); err != nil {
			return nil, err
		}
		usage["reasoning_tokens"] = c.reasoning
		var details map[string]json.RawMessage
		_ = json.Unmarshal(usage["output_tokens_details"], &details)
		if details == nil {
			details = make(map[string]json.RawMessage)
		}
		details["thinking_tokens"] = c.reasoning
		usage["output_tokens_details"], _ = json.Marshal(details)
		root["usage"], _ = json.Marshal(usage)
		return json.Marshal(root)
	}
	if !streaming {
		return patch(payload)
	}
	var out bytes.Buffer
	var decoder eventStreamDecoder
	_, err := decoder.feed(payload, func(event streamEvent) (bool, error) {
		data, err := patch(event.data)
		if err != nil {
			return false, err
		}
		out.Write(event.fields)
		out.WriteString("data: ")
		out.Write(data)
		out.WriteString("\n\n")
		return false, nil
	}, func() error {
		return failure("upstream_stream_too_large", "WorkBuddy response event exceeded the response limit", 502)
	})
	return out.Bytes(), err
}

// Validate explicit effort against the selected account before the SDK maps a
// level to a Messages token budget. That conversion must not erase the original
// level and thereby bypass the account's advertised set.
func prepareTranslatedControls(source string, payload []byte, body map[string]json.RawMessage, capabilities *CatalogModel) ([]byte, error) {
	if err := validateTranslatedOutput(source, body); err != nil {
		return nil, err
	}
	controls := make(map[string]json.RawMessage)
	changed := false
	switch source {
	case "openai", "openai-response":
		for _, key := range []string{"reasoning", "reasoning_effort"} {
			if raw, exists := body[key]; exists {
				controls[key] = raw
			}
		}
		if source == "openai" {
			var reasoning map[string]json.RawMessage
			if json.Unmarshal(body["reasoning"], &reasoning) == nil {
				if effort, exists := reasoning["effort"]; exists && nonemptyChatValue(effort) {
					if _, present := body["reasoning_effort"]; !present {
						body["reasoning_effort"] = effort
						changed = true
					}
				}
			}
		}
	case "claude":
		var output map[string]json.RawMessage
		if json.Unmarshal(body["output_config"], &output) == nil {
			if effort, exists := output["effort"]; exists {
				controls["reasoning_effort"] = effort
			}
		}
	case "gemini":
		var generation, thinking map[string]json.RawMessage
		if json.Unmarshal(body["generationConfig"], &generation) == nil && json.Unmarshal(generation["thinkingConfig"], &thinking) == nil {
			effort, exists := thinking["thinkingLevel"]
			if !exists {
				effort, exists = thinking["thinking_level"]
			}
			if exists {
				var level string
				if json.Unmarshal(effort, &level) == nil {
					effort, _ = json.Marshal(strings.ToLower(level))
				}
				controls["reasoning_effort"] = effort
			}
		}
	}
	if len(controls) > 0 {
		if err := applyChatEffort(controls, capabilities); err != nil {
			return nil, err
		}
	}
	if !changed {
		return payload, nil
	}
	return json.Marshal(body)
}

func validateTranslatedOutput(source string, body map[string]json.RawMessage) error {
	unsupported := func() error {
		return failure("unsupported_feature", "WorkBuddy execution supports text output, including reasoning and tools, but not image generation or audio output", 400)
	}
	for _, field := range []string{"generate", "image", "image_generation", "audio"} {
		if nonemptyChatValue(body[field]) {
			return unsupported()
		}
	}
	if source == "openai" && nonemptyChatValue(body["thinking"]) {
		return failure("unsupported_feature", "Chat thinking controls must use reasoning effort", 400)
	}
	modalities := body["modalities"]
	if source == "gemini" {
		var generation map[string]json.RawMessage
		if json.Unmarshal(body["generationConfig"], &generation) == nil {
			modalities = generation["responseModalities"]
		}
	}
	if nonemptyChatValue(modalities) {
		var values []string
		if json.Unmarshal(modalities, &values) != nil || len(values) != 1 || !strings.EqualFold(values[0], "text") {
			return unsupported()
		}
	}
	if source == "openai-response" {
		var tools []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(body["tools"], &tools) == nil {
			for _, tool := range tools {
				if tool.Type == "image_generation" {
					return unsupported()
				}
			}
		}
	}
	return nil
}
