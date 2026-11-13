package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"time"
)

// ModelInfo contains only the account metadata expressible by CPA. Execution
// capability fields are populated by the slices that implement those paths.
type ModelInfo struct {
	ID                  string
	Object              string
	OwnedBy             string
	Type                string
	DisplayName         string
	Name                string
	ContextLength       int64
	InputTokenLimit     int64
	OutputTokenLimit    int64
	MaxCompletionTokens int64
}

type ModelResponse struct {
	Provider   string
	Models     []ModelInfo
	AuthUpdate AuthData
}

type modelSnapshot struct {
	AuthID     string         `json:"auth_id"`
	Site       string         `json:"site"`
	CapturedAt time.Time      `json:"captured_at"`
	Models     []CatalogModel `json:"models"`
}

type authModelRequest struct {
	AuthID         string
	AuthProvider   string
	StorageJSON    []byte
	Metadata       map[string]any
	HostCallbackID string `json:"host_callback_id"`
}

func models(ctx context.Context, method string, raw []byte, call func(string, []byte) ([]byte, error)) (ModelResponse, error) {
	result := ModelResponse{Provider: provider, Models: []ModelInfo{}}
	var req authModelRequest
	if err := decode(raw, &req); err != nil {
		return result, err
	}
	if method == "model.static" {
		return result, nil
	}
	stored, metadata, handled, err := credentialFromJSON(req.StorageJSON)
	if err != nil {
		return result, err
	}
	if req.AuthProvider != provider || !handled {
		return result, failure("invalid_credentials", "model discovery requires WorkBuddy browser credentials", 400)
	}
	chosen, err := credentialSite(stored)
	if err != nil {
		return result, err
	}
	auth := AuthData{Provider: provider, ID: req.AuthID, StorageJSON: req.StorageJSON}
	headers, err := auth.RequestHeaders()
	if err != nil {
		return result, err
	}
	maps.Copy(metadata, req.Metadata)
	var config json.RawMessage
	err = request(ctx, chosen, req.HostCallbackID, call, "GET", "/v3/config", headers, nil, &config)
	if ctx.Err() != nil {
		return result, failure("model_discovery_cancelled", "model discovery was cancelled", 408)
	}
	var models []CatalogModel
	if err == nil {
		models, err = ParseModelConfig(chosen.id, config, time.Now())
	}
	status, reason := "success", "success"
	changed := false
	if err == nil {
		changed = stored.ModelDiscoveryFailure != "" || !validModelSnapshot(stored.ModelSnapshot, req.AuthID, chosen.id) || !reflect.DeepEqual(stored.ModelSnapshot.Models, models)
		if changed {
			stored.ModelSnapshot = &modelSnapshot{AuthID: req.AuthID, Site: chosen.id, CapturedAt: time.Now().UTC(), Models: models}
			stored.ModelDiscoveryFailure = ""
		}
		if len(models) == 0 {
			status = "empty"
		}
	} else {
		reason = discoveryReason(err)
		status = "unavailable"
		if reason == "auth_rejected" || reason == "missing_cli_config" || reason == "invalid_model_config" {
			changed = stored.ModelDiscoveryFailure != reason
			stored.ModelDiscoveryFailure = reason
		} else if reason == "temporary_failure" && stored.ModelDiscoveryFailure == "" && validModelSnapshot(stored.ModelSnapshot, req.AuthID, chosen.id) {
			models = stored.ModelSnapshot.Models
			status = "snapshot"
		}
	}
	for _, model := range models {
		result.Models = append(result.Models, ModelInfo{
			ID: chosen.modelPrefix + model.ID, Object: "model", OwnedBy: provider, Type: provider,
			DisplayName: model.Name, Name: model.ID, ContextLength: model.ContextLength,
			InputTokenLimit: model.ContextLength, OutputTokenLimit: model.MaxOutputTokens, MaxCompletionTokens: model.MaxOutputTokens,
		})
	}
	if changed {
		result.AuthUpdate, err = authFromCredential(stored, metadata)
		if err != nil {
			return result, err
		}
	}
	fields := map[string]any{"site": chosen.id, "auth_id": req.AuthID, "status": status, "reason": reason, "model_count": len(result.Models)}
	if status == "snapshot" {
		fields["snapshot_at"] = stored.ModelSnapshot.CapturedAt.Format(time.RFC3339Nano)
	}
	level := "info"
	if reason != "success" {
		level = "warn"
	}
	logRequest, err := json.Marshal(struct {
		HostCallbackID string         `json:"host_callback_id"`
		Level          string         `json:"level"`
		Message        string         `json:"message"`
		Fields         map[string]any `json:"fields"`
	}{req.HostCallbackID, level, "WorkBuddy model discovery", fields})
	if err != nil {
		return result, failure("internal_error", "cannot encode model discovery diagnostic", 500)
	}
	if call == nil {
		return result, failure("host_unavailable", "host diagnostic callback is unavailable", 503)
	}
	reply, err := call("host.log", logRequest)
	var envelope struct {
		OK bool `json:"ok"`
	}
	if err != nil || json.Unmarshal(reply, &envelope) != nil || !envelope.OK {
		return result, failure("host_log_failed", "cannot report model discovery diagnostic", 502)
	}
	if ctx.Err() != nil {
		return result, failure("model_discovery_cancelled", "model discovery was cancelled", 408)
	}
	return result, nil
}

func discoveryReason(err error) string {
	if upstream, ok := errors.AsType[*upstreamError](err); ok {
		if upstream.invalidToken {
			return "auth_rejected"
		}
		if upstream.status == 408 || upstream.status == 429 || upstream.status >= 500 {
			return "temporary_failure"
		}
		return "upstream_failure"
	}
	if detail, ok := errors.AsType[*Error](err); ok {
		switch detail.Code {
		case "missing_cli_config", "invalid_model_config":
			return detail.Code
		case "host_http_failed", "host_unavailable":
			return "temporary_failure"
		case "invalid_upstream":
			return "invalid_model_config"
		}
	}
	return "upstream_failure"
}

func validModelSnapshot(snapshot *modelSnapshot, authID, siteID string) bool {
	if snapshot == nil || snapshot.AuthID != authID || snapshot.Site != siteID || snapshot.CapturedAt.IsZero() || snapshot.CapturedAt.After(time.Now()) || snapshot.Models == nil {
		return false
	}
	seen := make(map[string]bool, len(snapshot.Models))
	for _, model := range snapshot.Models {
		if !catalogID(model.ID) || seen[model.ID] || model.ContextLength < 0 || model.MaxOutputTokens < 0 {
			return false
		}
		seen[model.ID] = true
	}
	return true
}
