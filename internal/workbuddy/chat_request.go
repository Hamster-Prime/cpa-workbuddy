package workbuddy

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// executorRequest retains the CPA SDK wire fields, including its lowercase RPC IDs.
type executorRequest struct {
	AuthID          string
	AuthProvider    string
	Model           string
	Format          string
	SourceFormat    string
	Stream          bool
	Payload         []byte
	OriginalRequest []byte
	StorageJSON     []byte
	Headers         map[string][]string
	Metadata        map[string]any
	AuthMetadata    map[string]any
	HostCallbackID  string `json:"host_callback_id"`
	StreamID        string `json:"stream_id"`
}

type preparedChat struct {
	request   hostRequest
	model     string
	toolNames map[string]string
}

const maxChatPayload = 32 << 20

var chatIDSalt = rand.Text()

type chatMessage struct {
	fields map[string]json.RawMessage
	role   string
	text   []string
	images []string
}

func prepareChat(req executorRequest) (preparedChat, error) {
	if req.Format != "openai" {
		return preparedChat{}, failure("unsupported_format", "chat execution requires the openai format", 400)
	}
	headers, err := (AuthData{ID: req.AuthID, Provider: req.AuthProvider, StorageJSON: req.StorageJSON}).RequestHeaders()
	if err != nil {
		return preparedChat{}, err
	}
	stored, _, _, err := credentialFromJSON(req.StorageJSON)
	if err != nil {
		return preparedChat{}, err
	}
	chosen, err := credentialSite(stored)
	if err != nil {
		return preparedChat{}, err
	}
	model, matches := strings.CutPrefix(req.Model, chosen.modelPrefix)
	if !matches || !catalogID(model) || strings.HasPrefix(model, "wb/") || strings.HasPrefix(model, "wb-ai/") {
		return preparedChat{}, failure("unsupported_model", "model does not belong to the selected WorkBuddy site", 400)
	}
	if messagesModel(chosen.id, model) {
		return preparedChat{}, failure("unsupported_model", "this WorkBuddy model requires Messages execution", 400)
	}
	if len(req.Payload) > maxChatPayload {
		return preparedChat{}, failure("request_too_large", "chat payload exceeds the 32 MiB limit", 413)
	}
	var body map[string]json.RawMessage
	if err := decode(req.Payload, &body); err != nil {
		return preparedChat{}, err
	}
	for _, field := range []string{"thinking", "generate", "image", "image_generation", "audio"} {
		if nonemptyChatValue(body[field]) {
			return preparedChat{}, failure("unsupported_feature", "Chat execution does not support image generation, audio, or thinking budgets", 400)
		}
	}
	if nonemptyChatValue(body["modalities"]) {
		var modalities []string
		if json.Unmarshal(body["modalities"], &modalities) != nil || len(modalities) != 1 || modalities[0] != "text" {
			return preparedChat{}, failure("unsupported_feature", "chat execution currently supports text output only", 400)
		}
	}
	capabilities := accountChatModel(stored, req.AuthID, model)
	if err := applyChatEffort(body, capabilities); err != nil {
		return preparedChat{}, err
	}
	allowImages := capabilities != nil && catalogImages(chosen.id, model, capabilities.Images)
	messages, err := readChatMessages(body["messages"], allowImages)
	if err != nil {
		return preparedChat{}, err
	}
	toolNames, err := fitChatTools(body, messages)
	if err != nil {
		return preparedChat{}, err
	}
	for i := range messages {
		if messages[i].role == "developer" {
			messages[i].fields["role"] = json.RawMessage(`"system"`)
			messages[i].role = "system"
		}
	}
	if messages[0].role != "system" {
		messages = append([]chatMessage{{fields: map[string]json.RawMessage{"role": json.RawMessage(`"system"`), "content": json.RawMessage(`"You are a helpful assistant."`)}, role: "system"}}, messages...)
	}
	attendChatRequest(headers, req, messages)
	wireMessages := make([]map[string]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		wireMessages = append(wireMessages, message.fields)
	}
	body["messages"], err = json.Marshal(wireMessages)
	if err != nil {
		return preparedChat{}, failure("invalid_request", "cannot encode chat messages", 400)
	}
	if limit, exists := body["max_completion_tokens"]; exists {
		if existing, present := body["max_tokens"]; !present || !chatHasJSONValue(existing) {
			body["max_tokens"] = limit
		}
		delete(body, "max_completion_tokens")
	}
	body["model"], err = json.Marshal(model)
	if err != nil {
		return preparedChat{}, failure("invalid_request", "cannot encode chat model", 400)
	}
	body["stream"] = json.RawMessage("true")
	options := make(map[string]json.RawMessage)
	if value, exists := body["stream_options"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		if err := decode(value, &options); err != nil {
			return preparedChat{}, failure("invalid_request", "stream_options must be a JSON object", 400)
		}
	}
	options["include_usage"] = json.RawMessage("true")
	body["stream_options"], err = json.Marshal(options)
	if err != nil {
		return preparedChat{}, failure("invalid_request", "cannot encode stream options", 400)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if err := json.NewEncoder(writer).Encode(body); err != nil {
		return preparedChat{}, failure("invalid_request", "cannot encode chat payload", 400)
	}
	if err := writer.Close(); err != nil {
		return preparedChat{}, failure("internal_error", "cannot compress chat payload", 500)
	}
	setChatHeaders(headers)
	return preparedChat{
		request:   hostRequest{Method: http.MethodPost, URL: chosen.endpoint + "/v2/chat/completions", Headers: headers, Body: compressed.Bytes(), HostCallbackID: req.HostCallbackID},
		model:     req.Model,
		toolNames: toolNames,
	}, nil
}

func nonemptyChatValue(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) >= 2 && ((raw[0] == '[' && raw[len(raw)-1] == ']') || (raw[0] == '{' && raw[len(raw)-1] == '}')) && len(bytes.TrimSpace(raw[1:len(raw)-1])) == 0 {
		return false
	}
	return len(raw) != 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte("false")) && !bytes.Equal(raw, []byte(`""`))
}

func readChatMessages(raw json.RawMessage, allowImages bool) ([]chatMessage, error) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return nil, failure("invalid_request", "chat messages must be a nonempty array of objects", 400)
	}
	messages := make([]chatMessage, 0, len(rows))
	for _, fields := range rows {
		var role string
		if json.Unmarshal(fields["role"], &role) != nil {
			return nil, failure("invalid_request", "chat message role is required", 400)
		}
		if role == "function" || nonemptyChatValue(fields["function_call"]) || nonemptyChatValue(fields["audio"]) {
			return nil, failure("unsupported_feature", "legacy function calls and audio are not supported by text chat execution", 400)
		}
		if role != "system" && role != "developer" && role != "user" && role != "assistant" && role != "tool" {
			return nil, failure("invalid_request", "unsupported chat message role", 400)
		}
		content := bytes.TrimSpace(fields["content"])
		var text []string
		var images []string
		if len(content) > 0 && content[0] == '"' {
			var value string
			if json.Unmarshal(content, &value) != nil {
				return nil, failure("invalid_request", "chat message content must contain text", 400)
			}
			if value != "" {
				text = []string{value}
			}
		} else if len(content) > 0 && content[0] == '[' {
			var parts []map[string]json.RawMessage
			if json.Unmarshal(content, &parts) != nil {
				return nil, failure("invalid_request", "chat message content must contain text", 400)
			}
			for _, part := range parts {
				var kind, value string
				if rawType, exists := part["type"]; exists && json.Unmarshal(rawType, &kind) != nil {
					return nil, failure("invalid_request", "invalid chat content type", 400)
				}
				if kind == "image_url" {
					if !allowImages {
						return nil, failure("unsupported_feature", "the selected account model does not advertise image input", 400)
					}
					if err := validateChatImagePart(part); err != nil {
						return nil, err
					}
					if role == "user" {
						digest := sha256.Sum256(part["image_url"])
						images = append(images, hex.EncodeToString(digest[:]))
					}
					continue
				}
				if kind != "" && kind != "text" {
					return nil, failure("unsupported_feature", "Chat execution supports text and advertised image_url content parts", 400)
				}
				if json.Unmarshal(part["text"], &value) != nil || bytes.Equal(bytes.TrimSpace(part["text"]), []byte("null")) {
					return nil, failure("invalid_request", "chat content parts require text strings", 400)
				}
				if value != "" {
					text = append(text, value)
				}
			}
		} else if role == "assistant" && (hasChatToolCalls(fields) || nonemptyChatValue(fields["reasoning"]) || nonemptyChatValue(fields["reasoning_content"]) || nonemptyChatValue(fields["reasoning_details"])) && (len(content) == 0 || bytes.Equal(content, []byte("null"))) {
			// Assistant tool calls and thinking history can have no visible text.
		} else {
			return nil, failure("invalid_request", "chat message content must be a string or content array", 400)
		}
		if role == "assistant" {
			if err := normalizeChatReasoningHistory(fields); err != nil {
				return nil, err
			}
		}
		messages = append(messages, chatMessage{fields: fields, role: role, text: text, images: images})
	}
	return messages, nil
}

func hasChatToolCalls(fields map[string]json.RawMessage) bool {
	raw := bytes.TrimSpace(fields["tool_calls"])
	return len(raw) > 2 && raw[0] == '[' && len(bytes.TrimSpace(raw[1:len(raw)-1])) != 0
}

func normalizeChatReasoningHistory(fields map[string]json.RawMessage) error {
	content, hasContent := fields["reasoning_content"]
	details, hasDetails := fields["reasoning_details"]
	if !hasContent && !hasDetails {
		return nil
	}
	readText := func(raw json.RawMessage) (string, error) {
		if len(raw) == 0 || raw[0] != '"' {
			return "", nil
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", failure("invalid_request", "invalid historical reasoning text", 400)
		}
		return text, nil
	}
	existing, err := readText(fields["reasoning"])
	if err != nil {
		return err
	}
	text, err := readText(content)
	if err != nil {
		return err
	}
	if existing == "" && text == "" && len(details) > 0 && details[0] == '[' {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(details, &rows); err != nil {
			return failure("invalid_request", "invalid historical reasoning details", 400)
		}
		var joined strings.Builder
		for _, row := range rows {
			raw := row["text"]
			if len(raw) == 0 || raw[0] != '"' {
				raw = row["summary"]
			}
			part, err := readText(raw)
			if err != nil {
				return err
			}
			joined.WriteString(part)
		}
		text = joined.String()
	}
	if existing == "" && text != "" {
		fields["reasoning"], err = json.Marshal(text)
		if err != nil {
			return failure("invalid_request", "cannot encode historical reasoning", 400)
		}
	}
	delete(fields, "reasoning_content")
	delete(fields, "reasoning_details")
	return nil
}

// A tool's reply, and a user attachment immediately after it, belong to the
// original user send. System messages between them do not open a new turn.
func answeredChatBefore(messages []chatMessage, i int) bool {
	previous := i - 1
	for previous >= 0 && messages[previous].role == "system" {
		previous--
	}
	return previous >= 0 && (messages[previous].role == "tool" || (messages[previous].role == "assistant" && hasChatToolCalls(messages[previous].fields)))
}

func setChatHeaders(headers map[string][]string) {
	headers["Content-Type"] = []string{"application/json"}
	headers["Content-Encoding"] = []string{"gzip"}
	headers["Accept"] = []string{"application/json"}
	headers["X-Agent-Type"] = []string{"main"}
	headers["X-Agent-Runtime"] = []string{"local-agent"}
	headers["X-Stainless-Retry-Count"] = []string{"0"}
	headers["X-Stainless-Timeout"] = []string{"1200"}
	headers["X-Stainless-Lang"] = []string{"js"}
	headers["X-Stainless-Package-Version"] = []string{"6.25.0"}
	headers["X-Stainless-Runtime"] = []string{"node"}
	headers["X-Stainless-Runtime-Version"] = []string{"v22.21.1"}
	var osName string
	switch runtime.GOOS {
	case "darwin":
		osName = "MacOS"
	case "windows":
		osName = "Windows"
	case "linux":
		osName = "Linux"
	case "freebsd":
		osName = "FreeBSD"
	case "openbsd":
		osName = "OpenBSD"
	case "android":
		osName = "Android"
	default:
		osName = "Other:" + runtime.GOOS
	}
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	case "arm":
		arch = "arm"
	case "386":
		arch = "x32"
	default:
		arch = "other:" + runtime.GOARCH
	}
	headers["X-Stainless-OS"] = []string{osName}
	headers["X-Stainless-Arch"] = []string{arch}
}

func chatHeader(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) != 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func chatSession(req executorRequest) string {
	// These are the pinned CPA executor metadata keys, not auth metadata or LCP routing affinity.
	for _, key := range []string{"canonical_session_id", "execution_session_id", "derived_session_id"} {
		if value, ok := req.Metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return boundedChatID(value)
		}
	}
	return boundedChatID(chatHeader(req.Headers, "x-magpie-workbuddy-session"))
}

func boundedChatID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func chatID(kind, account string, pieces ...string) string {
	digest := sha256.New()
	_, _ = io.WriteString(digest, chatIDSalt)
	_, _ = io.WriteString(digest, "|"+kind+"|account:"+account+"|")
	for _, piece := range pieces {
		_, _ = io.WriteString(digest, piece)
	}
	var sum [sha256.Size]byte
	return hex.EncodeToString(digest.Sum(sum[:0])[:16])
}

func (message chatMessage) identityParts(prefix ...string) []string {
	prefix = append(prefix, message.text...)
	if len(message.images) > 0 {
		prefix = append(prefix, "|images:")
		prefix = append(prefix, message.images...)
	}
	return prefix
}

func attendChatRequest(headers map[string][]string, req executorRequest, messages []chatMessage) {
	var conversation []string
	if session := chatSession(req); session != "" {
		conversation = []string{"session:", session}
	} else {
		for _, message := range messages {
			if message.role == "user" && len(message.text)+len(message.images) > 0 {
				conversation = message.identityParts("first:")
				break
			}
		}
	}
	conversationID := ""
	if explicit := boundedChatID(chatHeader(req.Headers, "X-Conversation-ID")); explicit != "" {
		conversationID = chatID("conversation", req.AuthID, "explicit:", explicit)
	} else if len(conversation) != 0 {
		conversationID = chatID("conversation", req.AuthID, conversation...)
	}
	if conversationID != "" {
		headers["X-Conversation-ID"] = []string{conversationID}
	}
	var turn string
	if explicit := boundedChatID(chatHeader(req.Headers, "X-Conversation-Request-ID")); explicit != "" {
		turn = chatID("turn", req.AuthID, conversationID, "|explicit:", explicit)
	} else {
		for i, message := range slices.Backward(messages) {
			if message.role != "user" || answeredChatBefore(messages, i) {
				continue
			}
			if len(message.text)+len(message.images) > 0 {
				pieces := message.identityParts(conversationID, "|u", strconv.Itoa(i), ":")
				turn = chatID("turn", req.AuthID, pieces...)
			}
			break
		}
	}
	var message [16]byte
	_, _ = rand.Read(message[:])
	messageID := hex.EncodeToString(message[:])
	if turn == "" {
		_, _ = rand.Read(message[:])
		turn = hex.EncodeToString(message[:])
	}
	headers["X-Conversation-Request-ID"] = []string{turn}
	headers["X-Conversation-Message-ID"] = []string{messageID}
	headers["X-Request-ID"] = []string{messageID}
}
