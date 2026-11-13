package workbuddy

import (
	"bytes"
	"encoding/json"
	"slices"
)

// accountChatModel reads only the selected identity's successful discovery.
// Global registry metadata cannot authorize another account's input capability.
func accountChatModel(stored credential, authID, modelID string) *CatalogModel {
	if stored.ModelDiscoveryFailure != "" || !validModelSnapshot(stored.ModelSnapshot, authID, stored.Site) {
		return nil
	}
	for i := range stored.ModelSnapshot.Models {
		if stored.ModelSnapshot.Models[i].ID == modelID {
			return &stored.ModelSnapshot.Models[i]
		}
	}
	return nil
}

func applyChatEffort(body map[string]json.RawMessage, model *CatalogModel) error {
	invalid := func() error {
		return failure("invalid_request", "Chat reasoning effort must name one supported account model level", 400)
	}
	var explicit string
	named := false
	readEffort := func(raw json.RawMessage) error {
		named = true
		if !nonemptyChatValue(raw) {
			return nil
		}
		var effort string
		if json.Unmarshal(raw, &effort) != nil || explicit != "" && effort != explicit {
			return invalid()
		}
		explicit = effort
		return nil
	}
	if raw, exists := body["reasoning_effort"]; exists {
		if err := readEffort(raw); err != nil {
			return err
		}
	}
	if raw := body["reasoning"]; nonemptyChatValue(raw) {
		var reasoning map[string]json.RawMessage
		if json.Unmarshal(raw, &reasoning) != nil || reasoning == nil {
			return invalid()
		}
		for _, value := range reasoning {
			if nonemptyChatValue(value) && (model == nil || len(model.Efforts) == 0) {
				return failure("unsupported_feature", "the selected account model does not advertise reasoning controls", 400)
			}
		}
		if raw, exists := reasoning["effort"]; exists {
			if err := readEffort(raw); err != nil {
				return err
			}
		}
	}
	if explicit != "" && (model == nil || !slices.Contains(model.Efforts, explicit)) {
		return failure("unsupported_feature", "the selected account model does not advertise the requested reasoning effort", 400)
	}
	if named {
		return nil
	}
	if model == nil || model.DefaultEffort == "" || !slices.Contains(model.Efforts, model.DefaultEffort) {
		return nil
	}
	limit, exists := body["max_tokens"]
	if !exists || !chatHasJSONValue(limit) {
		limit = body["max_completion_tokens"]
	}
	limit = bytes.TrimSpace(limit)
	if len(limit) > 0 && (limit[0] == '-' || limit[0] >= '0' && limit[0] <= '9') {
		var number json.Number
		if json.Unmarshal(limit, &number) != nil {
			return invalid()
		}
		value, err := number.Float64()
		if err != nil {
			return invalid()
		}
		if value > 0 && value <= 1024 {
			return nil
		}
	}
	encoded, err := json.Marshal(model.DefaultEffort)
	if err != nil {
		return failure("invalid_request", "cannot encode the model reasoning effort", 400)
	}
	body["reasoning_effort"] = encoded
	return nil
}

func validateChatImagePart(part map[string]json.RawMessage) error {
	var image map[string]json.RawMessage
	var address string
	if json.Unmarshal(part["image_url"], &image) != nil || image == nil || json.Unmarshal(image["url"], &address) != nil || address == "" {
		return failure("invalid_request", "Chat image_url parts require a nonempty URL string", 400)
	}
	if raw, exists := image["detail"]; exists {
		var detail string
		if json.Unmarshal(raw, &detail) != nil || detail == "" {
			return failure("invalid_request", "Chat image detail must be a string", 400)
		}
	}
	return nil
}
