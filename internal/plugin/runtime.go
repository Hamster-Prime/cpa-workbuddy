// Package plugin implements CPA's registration, management, and lifecycle RPC boundary.
package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	workbuddy "cpa-workbuddy"
)

// These wire contracts follow CPA sdk/pluginabi and sdk/pluginapi, ABI 1/schema 6.
// In particular Metadata and ManagementResponse retain CPA's capitalized fields.
const (
	ABIVersion    = 1
	SchemaVersion = 6
	Name          = "cpa-workbuddy"
	StatusPath    = "/v0/management/plugins/" + Name + "/status"
)

type metadata struct {
	Name             string
	Version          string
	Author           string
	GitHubRepository string
}

type registration struct {
	SchemaVersion uint32          `json:"schema_version"`
	Metadata      metadata        `json:"metadata"`
	Capabilities  map[string]bool `json:"capabilities"`
}

type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type managementRequest struct {
	Method string
	Path   string
}

type managementResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

type rpcError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type envelope struct {
	OK     bool      `json:"ok"`
	Result any       `json:"result,omitempty"`
	Error  *rpcError `json:"error,omitempty"`
}

// Runtime owns the registered native instance's lifecycle. It retains no host
// callback or C memory; Shutdown waits for any in-flight Handle to complete.
type Runtime struct {
	mu         sync.Mutex
	registered bool
	quiesced   bool
	closed     bool
}

func New() *Runtime {
	return &Runtime{}
}

// Handle dispatches CPA RPC methods and always returns a CPA JSON envelope.
func (r *Runtime) Handle(method string, raw []byte) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		if method == "plugin.shutdown" {
			return success(struct{}{})
		}
		return Failure("plugin_shutdown", "plugin has shut down", 503)
	}
	switch method {
	case "plugin.register", "plugin.reconfigure":
		var req lifecycleRequest
		if err := decodeObject(raw, &req); err != nil {
			return Failure("invalid_request", err.Error(), 400)
		}
		if req.SchemaVersion != SchemaVersion {
			return Failure("incompatible_schema", fmt.Sprintf("schema_version must be %d", SchemaVersion), 400)
		}
		r.registered = true
		r.quiesced = false
		return success(registration{
			SchemaVersion: SchemaVersion,
			Metadata: metadata{
				Name: Name, Version: workbuddy.Version(),
				Author:           "sheepyu",
				GitHubRepository: "ygq-future/cpa-workbuddy",
			},
			Capabilities: map[string]bool{"management_api": true},
		})
	case "plugin.quiesce":
		r.quiesced = true
		return success(struct{}{})
	case "plugin.shutdown":
		r.closed = true
		return success(struct{}{})
	case "management.register", "management.handle":
		if !r.registered {
			return Failure("not_registered", "plugin.register must succeed first", 409)
		}
		if r.quiesced {
			return Failure("plugin_quiesced", "plugin is quiesced", 503)
		}
		if method == "management.register" {
			var req struct{ BasePath string }
			if err := decodeObject(raw, &req); err != nil {
				return Failure("invalid_request", err.Error(), 400)
			}
			if req.BasePath != "/v0/management" {
				return Failure("invalid_request", "BasePath must be /v0/management", 400)
			}
			return success(struct {
				Routes []managementRequest `json:"routes"`
			}{Routes: []managementRequest{{Method: "GET", Path: StatusPath}}})
		}
		var req managementRequest
		if err := decodeObject(raw, &req); err != nil {
			return Failure("invalid_request", err.Error(), 400)
		}
		if req.Path != StatusPath {
			return Failure("route_not_found", "unknown management path", 404)
		}
		if req.Method != "GET" {
			return Failure("method_not_allowed", "status requires GET", 405)
		}
		body, err := json.Marshal(struct {
			Name          string `json:"name"`
			Version       string `json:"version"`
			ABIVersion    uint32 `json:"abi_version"`
			SchemaVersion uint32 `json:"schema_version"`
		}{Name: Name, Version: workbuddy.Version(), ABIVersion: ABIVersion, SchemaVersion: SchemaVersion})
		if err != nil {
			return Failure("internal_error", err.Error(), 500)
		}
		return success(managementResponse{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json; charset=utf-8"}},
			Body:       body,
		})
	default:
		return Failure("unknown_method", "unsupported method: "+method, 404)
	}
}

// Shutdown is idempotent and closes this instance to future RPC work.
func (r *Runtime) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

// Failure is also used at the C boundary, where invalid pointers/lengths must be
// rejected before a request can enter the JSON dispatcher.
func Failure(code, message string, status int) []byte {
	return marshal(envelope{Error: &rpcError{Code: code, Message: message, HTTPStatus: status}})
}

func success(result any) []byte {
	return marshal(envelope{OK: true, Result: result})
}

func marshal(value envelope) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"internal_error","message":"cannot encode RPC response","http_status":500}}`)
	}
	return raw
}

func decodeObject(raw []byte, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("request must be a JSON object")
	}
	if err := json.Unmarshal(trimmed, target); err != nil {
		return fmt.Errorf("invalid request JSON: %w", err)
	}
	return nil
}
