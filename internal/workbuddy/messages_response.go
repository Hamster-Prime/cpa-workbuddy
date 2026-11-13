package workbuddy

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

type messagesBlock struct {
	kind       string
	stopped    bool
	fields     map[string]json.RawMessage
	text       strings.Builder
	signature  strings.Builder
	input      bytes.Buffer
	inputJSON  messagesJSON
	inputDelta bool
	inputEmpty bool
	signed     bool
}

// consumeMessagesStream preserves native event order. Only aggregation retains
// text, signatures and argument bodies; streaming retains bounded protocol state.
func consumeMessagesStream(read func() ([]byte, bool, error), model string, emit func([]byte) error) ([]byte, error) {
	malformed := func() error {
		return failure("upstream_stream_malformed", "WorkBuddy returned an invalid Messages stream", 502)
	}
	incomplete := func() error {
		return failure("upstream_stream_incomplete", "WorkBuddy Messages stream ended before completion", 502)
	}
	tooLarge := func() error {
		return failure("upstream_stream_too_large", "WorkBuddy Messages stream exceeded the response limit", 502)
	}
	unsupported := func() error {
		return failure("unsupported_upstream_response", "WorkBuddy returned an unsupported Messages response", 502)
	}
	modelJSON, _ := json.Marshal(model)
	var decoder eventStreamDecoder
	var started, finishing, finished, stopped bool
	var terminalFrame []byte
	var top map[string]json.RawMessage
	var usage map[string]json.RawMessage
	toolIDs := make(map[string]bool)
	blocks := make(map[int]*messagesBlock)
	size := 0
	charge := func(n int) error {
		size += n
		if size > chatAggregateLimit {
			return tooLarge()
		}
		return nil
	}
	merge := func(dst map[string]json.RawMessage, src map[string]json.RawMessage) error {
		for key, value := range src {
			n := len(value) - len(dst[key])
			if _, exists := dst[key]; !exists {
				n += len(key) + 64
			}
			if err := charge(n); err != nil {
				return err
			}
			dst[key] = value
		}
		return nil
	}
	process := func(event streamEvent) (bool, error) {
		if !event.hasData {
			if event.name == "error" {
				return false, failure("upstream_stream_error", "WorkBuddy Messages stream failed", 502)
			}
			if event.name != "" {
				return false, malformed()
			}
			return false, nil
		}
		var chunk map[string]json.RawMessage
		if !utf8.Valid(event.data) || json.Unmarshal(event.data, &chunk) != nil || chunk == nil {
			return false, malformed()
		}
		if stopped {
			return false, malformed()
		}
		var kind string
		validKind := json.Unmarshal(chunk["type"], &kind) == nil && kind != ""
		if event.name == "error" || kind == "error" || chatHasJSONValue(chunk["error"]) || chatHasErrorCode(chunk["code"]) {
			if chatClientRejected(chunk) {
				return false, failure("upstream_client_version_rejected", "WorkBuddy client version rejected", 502)
			}
			return false, failure("upstream_stream_error", "WorkBuddy Messages stream failed", 502)
		}
		if !validKind || event.name != "" && event.name != kind {
			return false, malformed()
		}
		switch kind {
		case "ping":
		case "message_start":
			if started {
				return false, malformed()
			}
			message, ok := messagesObject(chunk["message"])
			if !ok || !messagesString(message["id"], true) || !messagesValue(message["type"], "message") || !messagesValue(message["role"], "assistant") || !messagesString(message["model"], true) {
				return false, malformed()
			}
			var content []json.RawMessage
			if json.Unmarshal(message["content"], &content) != nil || content == nil || len(content) != 0 || chatHasJSONValue(message["stop_reason"]) || chatHasJSONValue(message["stop_sequence"]) {
				return false, malformed()
			}
			initialUsage, ok := messagesUsage(message["usage"])
			if !ok {
				return false, malformed()
			}
			started = true
			message["model"] = modelJSON
			if emit == nil {
				top, usage = make(map[string]json.RawMessage), make(map[string]json.RawMessage)
				delete(message, "usage")
				if err := merge(top, message); err != nil {
					return false, err
				}
				if err := merge(usage, initialUsage); err != nil {
					return false, err
				}
			} else {
				encoded, err := json.Marshal(message)
				if err != nil {
					return false, malformed()
				}
				chunk["message"] = encoded
			}
		case "content_block_start", "content_block_delta", "content_block_stop":
			if !started || finishing {
				return false, malformed()
			}
			var index int
			if !chatHasJSONValue(chunk["index"]) || json.Unmarshal(chunk["index"], &index) != nil || index < 0 {
				return false, malformed()
			}
			block := blocks[index]
			if kind == "content_block_start" {
				if block != nil {
					return false, malformed()
				}
				fields, ok := messagesObject(chunk["content_block"])
				var blockKind string
				if !ok || json.Unmarshal(fields["type"], &blockKind) != nil || blockKind == "" {
					return false, malformed()
				}
				if err := charge(512 + len(blockKind)); err != nil {
					return false, err
				}
				block = &messagesBlock{kind: blockKind}
				if emit == nil {
					block.fields = make(map[string]json.RawMessage)
					if err := merge(block.fields, fields); err != nil {
						return false, err
					}
				}
				switch blockKind {
				case "text", "thinking":
					key := blockKind
					if key == "thinking" {
						var signature string
						if raw, exists := fields["signature"]; exists {
							var ok bool
							signature, ok = messagesText(raw)
							if !ok {
								return false, malformed()
							}
						}
						block.signed = signature != ""
						if emit == nil {
							block.signature.WriteString(signature)
						}
					}
					text, ok := messagesText(fields[key])
					if !ok {
						return false, malformed()
					}
					if emit == nil {
						block.text.WriteString(text)
					}
				case "tool_use":
					input := bytes.TrimSpace(fields["input"])
					id, ok := messagesText(fields["id"])
					if len(input) < 2 || input[0] != '{' || !ok || id == "" || !messagesString(fields["name"], true) {
						return false, malformed()
					}
					block.inputEmpty = len(bytes.TrimSpace(input[1:len(input)-1])) == 0
					if toolIDs[id] {
						return false, malformed()
					}
					if err := charge(len(id) + 64); err != nil {
						return false, err
					}
					toolIDs[id] = true
				case "redacted_thinking":
					if !messagesString(fields["data"], true) {
						return false, malformed()
					}
				}
				blocks[index] = block
			} else {
				if block == nil || block.stopped {
					return false, malformed()
				}
				if kind == "content_block_stop" {
					if block.inputDelta && !block.inputJSON.complete() || block.kind == "thinking" && !block.signed {
						return false, malformed()
					}
					block.stopped = true
					block.inputJSON = messagesJSON{}
				} else {
					delta, ok := messagesObject(chunk["delta"])
					var deltaKind, key string
					if !ok || json.Unmarshal(delta["type"], &deltaKind) != nil {
						return false, malformed()
					}
					switch deltaKind {
					case "text_delta":
						if block.kind != "text" {
							return false, malformed()
						}
						key = "text"
					case "thinking_delta":
						if block.kind != "thinking" || block.signed {
							return false, malformed()
						}
						key = "thinking"
					case "signature_delta":
						if block.kind != "thinking" {
							return false, malformed()
						}
						key = "signature"
					case "input_json_delta":
						if block.kind != "tool_use" {
							return false, malformed()
						}
						key = "partial_json"
					default:
						return false, unsupported()
					}
					text, ok := messagesText(delta[key])
					if !ok {
						return false, malformed()
					}
					for field, value := range delta {
						if field != "type" && field != key && chatHasJSONValue(value) {
							return false, unsupported()
						}
					}
					if key == "partial_json" && text != "" {
						block.inputDelta = true
						if !block.inputEmpty || !block.inputJSON.feed(text) {
							return false, malformed()
						}
					} else if key == "signature" && text != "" {
						block.signed = true
					}
					if emit == nil {
						n := len(text)
						if key != "partial_json" {
							n = chatEncodedTextSize(text)
						}
						if err := charge(n); err != nil {
							return false, err
						}
						switch key {
						case "partial_json":
							block.input.WriteString(text)
						case "signature":
							block.signature.WriteString(text)
						default:
							block.text.WriteString(text)
						}
					}
				}
			}
		case "message_delta":
			if !started || finished {
				return false, malformed()
			}
			for _, block := range blocks {
				if !block.stopped {
					return false, malformed()
				}
			}
			delta, ok := messagesObject(chunk["delta"])
			if !ok || chatHasJSONValue(delta["stop_reason"]) && !messagesString(delta["stop_reason"], true) || chatHasJSONValue(delta["stop_sequence"]) && !messagesString(delta["stop_sequence"], false) {
				return false, malformed()
			}
			for _, key := range []string{"content", "model", "id", "type", "role", "usage"} {
				if _, exists := delta[key]; exists {
					return false, unsupported()
				}
			}
			var finalUsage map[string]json.RawMessage
			if raw, exists := chunk["usage"]; exists {
				finalUsage, ok = messagesUsage(raw)
				if !ok {
					return false, malformed()
				}
			}
			if emit == nil {
				if err := merge(top, delta); err != nil {
					return false, err
				}
				if err := merge(usage, finalUsage); err != nil {
					return false, err
				}
			}
			finished = chatHasJSONValue(delta["stop_reason"])
			finishing = true
		case "message_stop":
			if !finished {
				return false, incomplete()
			}
			stopped = true
		default:
			return false, unsupported()
		}
		if emit == nil {
			for key, value := range chunk {
				if !messagesEventField(kind, key) && chatHasJSONValue(value) {
					return false, unsupported()
				}
			}
		} else {
			encoded, err := json.Marshal(chunk)
			if err != nil {
				return false, malformed()
			}
			frame := make([]byte, 0, len(event.fields)+len(encoded)+8)
			frame = append(frame, event.fields...)
			frame = append(frame, "data: "...)
			frame = append(frame, encoded...)
			frame = append(frame, '\n', '\n')
			if stopped {
				terminalFrame = frame
			} else if err := emit(frame); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	for !stopped {
		payload, terminal, err := read()
		if _, decodeErr := decoder.feed(payload, process, tooLarge); decodeErr != nil {
			return nil, decodeErr
		}
		if stopped {
			// A terminal event ends the lease. Reject any partially buffered
			// additional event in the same borrowed transport payload as well.
			if len(decoder.line) != 0 || decoder.hasData || len(decoder.fields) != 0 {
				return nil, malformed()
			}
			if emit != nil {
				if err := emit(terminalFrame); err != nil {
					return nil, err
				}
			}
			break
		}
		if err != nil {
			return nil, err
		}
		if terminal {
			return nil, incomplete()
		}
	}
	if emit != nil {
		return nil, nil
	}
	indexes := make([]int, 0, len(blocks))
	for index := range blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	content := make([]map[string]json.RawMessage, 0, len(indexes))
	for _, index := range indexes {
		block := blocks[index]
		switch block.kind {
		case "text", "thinking":
			block.fields[block.kind], _ = json.Marshal(block.text.String())
			if block.kind == "thinking" {
				block.fields["signature"], _ = json.Marshal(block.signature.String())
			}
		case "tool_use":
			if block.inputDelta {
				block.fields["input"] = json.RawMessage(block.input.Bytes())
			}
		}
		content = append(content, block.fields)
	}
	top["content"], _ = json.Marshal(content)
	top["usage"], _ = json.Marshal(usage)
	result, err := json.Marshal(top)
	if err != nil {
		return nil, malformed()
	}
	if len(result) > chatAggregateLimit {
		return nil, tooLarge()
	}
	return result, nil
}

func messagesObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	err := json.Unmarshal(raw, &object)
	return object, err == nil && object != nil
}

func messagesString(raw json.RawMessage, nonempty bool) bool {
	var text string
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && json.Unmarshal(raw, &text) == nil && (!nonempty || text != "")
}

func messagesValue(raw json.RawMessage, want string) bool {
	var text string
	return json.Unmarshal(raw, &text) == nil && text == want
}

func messagesText(raw json.RawMessage) (string, bool) {
	var text string
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return text, true
}

func messagesEventField(kind, key string) bool {
	if key == "type" {
		return true
	}
	switch kind {
	case "message_start":
		return key == "message"
	case "content_block_start":
		return key == "index" || key == "content_block"
	case "content_block_delta":
		return key == "index" || key == "delta"
	case "content_block_stop":
		return key == "index"
	case "message_delta":
		return key == "delta" || key == "usage"
	}
	return false
}

// Usage fields are independent upstream counters. Preserve cache and reasoning
// details verbatim rather than deriving totals or replacing the start object.
func messagesUsage(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	object, ok := messagesObject(raw)
	if !ok {
		return nil, false
	}
	for key, value := range object {
		if strings.HasSuffix(key, "_tokens") {
			var n int64
			if !chatHasJSONValue(value) || json.Unmarshal(value, &n) != nil || n < 0 {
				return nil, false
			}
		}
	}
	return object, true
}

// messagesJSON validates an object across arbitrarily fragmented tool deltas.
// Its memory depends only on JSON nesting, never on argument body length.
type messagesJSON struct {
	stack   []byte
	started bool
	done    bool
	token   byte
	number  byte
	key     bool
	escape  bool
	unicode int
	literal string
}

func (j *messagesJSON) complete() bool {
	return j.done && j.token == 0
}

func (j *messagesJSON) feed(fragment string) bool {
	for i := range len(fragment) {
		b := fragment[i]
		if j.token == 's' {
			if j.unicode > 0 {
				if (b < '0' || b > '9') && (b < 'a' || b > 'f') && (b < 'A' || b > 'F') {
					return false
				}
				j.unicode--
			} else if j.escape {
				j.escape = false
				if b == 'u' {
					j.unicode = 4
				} else if !strings.ContainsRune(`"\/bfnrt`, rune(b)) {
					return false
				}
			} else if b == '\\' {
				j.escape = true
			} else if b == '"' {
				j.token = 0
				if j.key {
					j.stack[len(j.stack)-1] = ':'
				}
			} else if b < 0x20 {
				return false
			}
			continue
		}
		if j.token == 'l' {
			if len(j.literal) == 0 || b != j.literal[0] {
				return false
			}
			j.literal = j.literal[1:]
			if j.literal == "" {
				j.token = 0
			}
			continue
		}
		if j.token == 'n' {
			next := byte(0)
			switch j.number {
			case '-':
				if b == '0' {
					next = 'z'
				} else if b >= '1' && b <= '9' {
					next = 'i'
				}
			case 'z', 'i', 'f':
				if b >= '0' && b <= '9' && j.number != 'z' {
					next = j.number
				} else if b == '.' && j.number != 'f' {
					next = '.'
				} else if b == 'e' || b == 'E' {
					next = 'e'
				}
			case '.':
				if b >= '0' && b <= '9' {
					next = 'f'
				}
			case 'e':
				if b == '+' || b == '-' {
					next = '+'
				} else if b >= '0' && b <= '9' {
					next = 'x'
				}
			case '+', 'x':
				if b >= '0' && b <= '9' {
					next = 'x'
				}
			}
			if next != 0 {
				j.number = next
				continue
			}
			if j.number != 'z' && j.number != 'i' && j.number != 'f' && j.number != 'x' {
				return false
			}
			j.token = 0
		}
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			continue
		}
		if j.done {
			return false
		}
		if !j.started {
			if b != '{' {
				return false
			}
			j.started = true
			j.stack = append(j.stack, 'k')
			continue
		}
		last := len(j.stack) - 1
		state := j.stack[last]
		closeContainer := false
		switch state {
		case 'k', 'K':
			if b == '}' && state == 'k' {
				closeContainer = true
			} else if b == '"' {
				j.token, j.key = 's', true
				continue
			} else {
				return false
			}
		case ':':
			if b != ':' {
				return false
			}
			j.stack[last] = 'v'
			continue
		case 'o', 'c':
			if b == ',' {
				if state == 'o' {
					j.stack[last] = 'K'
				} else {
					j.stack[last] = 'A'
				}
				continue
			}
			if b == '}' && state == 'o' || b == ']' && state == 'c' {
				closeContainer = true
			} else {
				return false
			}
		case 'a':
			closeContainer = b == ']'
		}
		if closeContainer {
			j.stack = j.stack[:last]
			j.done = len(j.stack) == 0
			continue
		}
		if state != 'v' && state != 'a' && state != 'A' {
			return false
		}
		if state == 'v' {
			j.stack[last] = 'o'
		} else {
			j.stack[last] = 'c'
		}
		switch b {
		case '{', '[':
			if len(j.stack) == 10000 {
				return false
			}
			if b == '{' {
				j.stack = append(j.stack, 'k')
			} else {
				j.stack = append(j.stack, 'a')
			}
		case '"':
			j.token, j.key = 's', false
		case 't', 'f', 'n':
			j.token = 'l'
			switch b {
			case 't':
				j.literal = "rue"
			case 'f':
				j.literal = "alse"
			case 'n':
				j.literal = "ull"
			}
		case '-':
			j.token, j.number = 'n', '-'
		default:
			if b < '0' || b > '9' {
				return false
			}
			j.token, j.number = 'n', 'i'
			if b == '0' {
				j.number = 'z'
			}
		}
	}
	return true
}
