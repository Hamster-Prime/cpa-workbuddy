package workbuddy

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// Reasoning aliases are independent upstream fields, never visible content.
// Details retain their own identity and metadata instead of flattening to text.
type chatReasoningResponse struct {
	content     strings.Builder
	reasoning   strings.Builder
	contentSeen bool
	reasonSeen  bool
	detailsSeen bool
	details     []*chatReasoningDetail
	byIndex     map[int]*chatReasoningDetail
	byID        map[string]*chatReasoningDetail
}

type chatReasoningDetail struct {
	index     int
	id        string
	fields    map[string]json.RawMessage
	fragments map[string]*strings.Builder
}

func chatReasoningMalformed() error {
	return failure("upstream_stream_malformed", "WorkBuddy returned invalid reasoning fragments", 502)
}

func chatReserveReasoning(size *int, amount int) error {
	if amount > chatAggregateLimit-*size {
		return failure("upstream_stream_too_large", "WorkBuddy reasoning exceeded the response limit", 502)
	}
	*size += amount
	return nil
}

func (choice *chatChoice) addReasoningDelta(key string, raw json.RawMessage, aggregate bool, size *int) error {
	if !chatHasJSONValue(raw) {
		return nil
	}
	if key != "reasoning_details" {
		var text string
		if json.Unmarshal(raw, &text) != nil || choice.finished && text != "" {
			return chatReasoningMalformed()
		}
		if !aggregate {
			return nil
		}
		if choice.reasoning == nil {
			if err := chatReserveReasoning(size, 192); err != nil {
				return err
			}
			choice.reasoning = &chatReasoningResponse{}
		}
		reasoning := choice.reasoning
		target, seen := &reasoning.reasoning, &reasoning.reasonSeen
		if key == "reasoning_content" {
			target, seen = &reasoning.content, &reasoning.contentSeen
		}
		amount := chatEncodedTextSize(text)
		if !*seen {
			amount += len(key) + 64
		}
		if err := chatReserveReasoning(size, amount); err != nil {
			return err
		}
		*seen = true
		target.WriteString(text)
		return nil
	}

	var deltas []json.RawMessage
	if json.Unmarshal(raw, &deltas) != nil {
		return chatReasoningMalformed()
	}
	if choice.reasoning == nil {
		if err := chatReserveReasoning(size, 192); err != nil {
			return err
		}
		choice.reasoning = &chatReasoningResponse{}
	}
	reasoning := choice.reasoning
	if !reasoning.detailsSeen {
		if err := chatReserveReasoning(size, 96); err != nil {
			return err
		}
		reasoning.detailsSeen = true
	}
	for _, rawDelta := range deltas {
		var delta map[string]json.RawMessage
		if json.Unmarshal(rawDelta, &delta) != nil || delta == nil {
			return chatReasoningMalformed()
		}
		index, id := -1, ""
		if value := delta["index"]; chatHasJSONValue(value) {
			if json.Unmarshal(value, &index) != nil || index < 0 {
				return chatReasoningMalformed()
			}
		}
		for _, key := range []string{"id", "type", "format"} {
			if value := delta[key]; chatHasJSONValue(value) {
				var text string
				if json.Unmarshal(value, &text) != nil {
					return chatReasoningMalformed()
				}
				if key == "id" {
					id = text
				}
			}
		}
		if choice.finished {
			for _, value := range delta {
				value = bytes.TrimSpace(value)
				if chatHasJSONValue(value) && !bytes.Equal(value, []byte(`""`)) && !bytes.Equal(value, []byte(`[]`)) && !bytes.Equal(value, []byte(`{}`)) {
					return chatReasoningMalformed()
				}
			}
		}
		if !aggregate && index < 0 && id == "" {
			// Anonymous details cannot be referenced by a later fragment.
			// Validate their strings without retaining aggregate-only state.
			for _, key := range []string{"text", "summary", "signature", "data"} {
				if value := delta[key]; chatHasJSONValue(value) {
					var text string
					if json.Unmarshal(value, &text) != nil {
						return chatReasoningMalformed()
					}
				}
			}
			continue
		}
		detail := reasoning.byIndex[index]
		if identified := reasoning.byID[id]; id != "" && identified != nil {
			if detail != nil && detail != identified {
				return chatReasoningMalformed()
			}
			detail = identified
		}
		if detail == nil {
			if err := chatReserveReasoning(size, 256); err != nil {
				return err
			}
			detail = &chatReasoningDetail{index: -1, fields: make(map[string]json.RawMessage)}
			if aggregate {
				reasoning.details = append(reasoning.details, detail)
			}
		}
		if index >= 0 {
			if detail.index >= 0 && detail.index != index {
				return chatReasoningMalformed()
			}
			if detail.index < 0 {
				if err := chatReserveReasoning(size, 64); err != nil {
					return err
				}
				if reasoning.byIndex == nil {
					reasoning.byIndex = make(map[int]*chatReasoningDetail)
				}
				detail.index = index
				reasoning.byIndex[index] = detail
			}
		}
		if id != "" {
			if detail.id != "" && detail.id != id {
				return chatReasoningMalformed()
			}
			if detail.id == "" {
				if err := chatReserveReasoning(size, len(id)+64); err != nil {
					return err
				}
				if reasoning.byID == nil {
					reasoning.byID = make(map[string]*chatReasoningDetail)
				}
				detail.id = id
				reasoning.byID[id] = detail
			}
		}
		if err := detail.addFields(delta, aggregate, size); err != nil {
			return err
		}
	}
	return nil
}

func (detail *chatReasoningDetail) addFields(delta map[string]json.RawMessage, aggregate bool, size *int) error {
	for key, value := range delta {
		identity := key == "id" || key == "type" || key == "format"
		fragment := key == "text" || key == "summary" || key == "signature" || key == "data"
		if fragment && chatHasJSONValue(value) {
			var text string
			if json.Unmarshal(value, &text) != nil {
				return chatReasoningMalformed()
			}
			if !aggregate {
				continue
			}
			target := detail.fragments[key]
			amount := chatEncodedTextSize(text)
			if target == nil {
				amount += len(key) + 96
			}
			if err := chatReserveReasoning(size, amount); err != nil {
				return err
			}
			if target == nil {
				if detail.fragments == nil {
					detail.fragments = make(map[string]*strings.Builder)
				}
				target = &strings.Builder{}
				detail.fragments[key] = target
			}
			target.WriteString(text)
			continue
		}
		previous, exists := detail.fields[key]
		if exists {
			if identity && bytes.Equal(bytes.TrimSpace(value), []byte(`""`)) {
				continue
			}
			if !chatHasJSONValue(value) || chatReasoningEqual(previous, value) {
				continue
			}
			// Null and empty identity fields can precede their actual metadata.
			if chatHasJSONValue(previous) && (!identity || !bytes.Equal(bytes.TrimSpace(previous), []byte(`""`))) {
				return chatReasoningMalformed()
			}
		}
		amount := chatReasoningRawSize(value) - chatReasoningRawSize(previous)
		if !exists {
			amount += len(key) + 96
		}
		if err := chatReserveReasoning(size, amount); err != nil {
			return err
		}
		detail.fields[key] = value
	}
	return nil
}

// Opaque structured metadata is preserved, including provider-specific fields.
// Repeated metadata must agree; conflicting values have no defined merge rule.
func chatReasoningEqual(left, right json.RawMessage) bool {
	if bytes.Equal(left, right) {
		return true
	}
	var a, b any
	decoder := json.NewDecoder(bytes.NewReader(left))
	decoder.UseNumber()
	if decoder.Decode(&a) != nil {
		return false
	}
	decoder = json.NewDecoder(bytes.NewReader(right))
	decoder.UseNumber()
	return decoder.Decode(&b) == nil && reflect.DeepEqual(a, b)
}

// Raw metadata is re-encoded with HTML escaping by encoding/json at output.
func chatReasoningRawSize(raw json.RawMessage) int {
	size := len(raw)
	for i := 0; i < len(raw); i++ {
		if raw[i] == '<' || raw[i] == '>' || raw[i] == '&' {
			size += 5
		} else if raw[i] == 0xe2 && i+2 < len(raw) && raw[i+1] == 0x80 && (raw[i+2] == 0xa8 || raw[i+2] == 0xa9) {
			size += 3
			i += 2
		}
	}
	return size
}

func (reasoning *chatReasoningResponse) addMessageFields(message map[string]any) {
	if reasoning.contentSeen {
		message["reasoning_content"] = reasoning.content.String()
	}
	if reasoning.reasonSeen {
		message["reasoning"] = reasoning.reasoning.String()
	}
	if reasoning.detailsSeen {
		details := make([]map[string]any, 0, len(reasoning.details))
		for _, detail := range reasoning.details {
			fields := make(map[string]any, len(detail.fields)+len(detail.fragments))
			for key, value := range detail.fields {
				fields[key] = value
			}
			for key, text := range detail.fragments {
				fields[key] = text.String()
			}
			details = append(details, fields)
		}
		message["reasoning_details"] = details
	}
}
