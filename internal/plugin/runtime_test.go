package plugin_test

import (
	"encoding/json"
	"testing"

	workbuddy "cpa-workbuddy"
	"cpa-workbuddy/internal/plugin"
)

func TestPublicRegistrationAndLifecycle(t *testing.T) {
	runtime := plugin.New()
	call := func(method, request string) map[string]json.RawMessage {
		t.Helper()
		var response map[string]json.RawMessage
		if err := json.Unmarshal(runtime.Handle(method, []byte(request)), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	assertError := func(method, request, code string) {
		t.Helper()
		response := call(method, request)
		var detail struct{ Code string }
		if err := json.Unmarshal(response["error"], &detail); err != nil {
			t.Fatal(err)
		}
		if string(response["ok"]) != "false" || detail.Code != code {
			t.Fatalf("%s: got %s, want error %s", method, response, code)
		}
	}
	assertError("management.handle", `{}`, "not_registered")
	assertError("auth.identifier", `{}`, "not_registered")
	for _, request := range []string{"", "null", "[]", "{", `{"schema_version":6} trailing`, `{"schema_version":6,"config_yaml":"bad%base64"}`} {
		assertError("plugin.register", request, "invalid_request")
	}
	for _, request := range []string{`{}`, `{"schema_version":1}`, `{"schema_version":7}`} {
		assertError("plugin.register", request, "incompatible_schema")
	}
	response := call("plugin.register", `{"schema_version":6,"config_yaml":null}`)
	var result struct {
		SchemaVersion int `json:"schema_version"`
		Metadata      struct{ Name, Version, Author, GitHubRepository string }
		Capabilities  map[string]json.RawMessage
	}
	if err := json.Unmarshal(response["result"], &result); err != nil {
		t.Fatal(err)
	}
	if string(response["ok"]) != "true" || result.SchemaVersion != 6 || result.Metadata.Name != "cpa-workbuddy" || result.Metadata.Version != workbuddy.Version() {
		t.Fatalf("unexpected registration: %s", response)
	}
	if result.Metadata.Author == "" || result.Metadata.GitHubRepository == "" {
		t.Fatalf("metadata must meet CPA validPlugin: %+v", result.Metadata)
	}
	if len(result.Capabilities) != 7 || string(result.Capabilities["management_api"]) != "true" || string(result.Capabilities["auth_provider"]) != "true" || string(result.Capabilities["model_provider"]) != "true" || string(result.Capabilities["executor"]) != "true" || string(result.Capabilities["executor_model_scope"]) != `"oauth"` || string(result.Capabilities["executor_input_formats"]) != `["openai"]` || string(result.Capabilities["executor_output_formats"]) != `["openai"]` {
		t.Fatalf("unexpected capabilities: %v", result.Capabilities)
	}
	assertError("plugin.identifier", `{}`, "unknown_method")
	identifier := call("auth.identifier", `{}`)
	if string(identifier["ok"]) != "true" || string(identifier["result"]) != `{"identifier":"workbuddy"}` {
		t.Fatalf("unexpected auth identifier: %s", identifier)
	}
	if string(call("executor.identifier", `{}`)["result"]) != `{"identifier":"workbuddy"}` {
		t.Fatal("executor identifier is invalid")
	}
	static := call("model.static", `{}`)
	var catalog struct{ Models []json.RawMessage }
	if err := json.Unmarshal(static["result"], &catalog); err != nil || string(static["ok"]) != "true" || catalog.Models == nil || len(catalog.Models) != 0 {
		t.Fatalf("static models granted routing permissions: %s", static)
	}
	assertError("management.register", `{}`, "invalid_request")
	assertError("management.handle", `{"Method":"GET","Path":"/missing"}`, "route_not_found")
	assertError("management.handle", `{"Method":"POST","Path":"/v0/management/plugins/cpa-workbuddy/status"}`, "method_not_allowed")
	if string(call("plugin.quiesce", `{}`)["ok"]) != "true" {
		t.Fatal("quiesce failed")
	}
	assertError("management.handle", `{}`, "plugin_quiesced")
	if string(call("plugin.reconfigure", `{"schema_version":6,"config_yaml":"eDogMQo="}`)["ok"]) != "true" {
		t.Fatal("reconfigure failed")
	}
	response = call("management.register", `{"BasePath":"/v0/management","ResourceBasePath":"/v0/resource/plugins/cpa-workbuddy"}`)
	var routes struct {
		Routes []struct{ Method, Path string }
	}
	if err := json.Unmarshal(response["result"], &routes); err != nil {
		t.Fatal(err)
	}
	if len(routes.Routes) != 1 || routes.Routes[0].Method != "GET" || routes.Routes[0].Path != "/v0/management/plugins/cpa-workbuddy/status" {
		t.Fatalf("unexpected route: %s", response)
	}
	response = call("management.handle", `{"Method":"GET","Path":"/v0/management/plugins/cpa-workbuddy/status"}`)
	var statusResponse struct {
		StatusCode int
		Headers    map[string][]string
		Body       []byte
	}
	if err := json.Unmarshal(response["result"], &statusResponse); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Name, Version string
		ABIVersion    int `json:"abi_version"`
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(statusResponse.Body, &status); err != nil {
		t.Fatal(err)
	}
	if statusResponse.StatusCode != 200 || len(statusResponse.Headers["Content-Type"]) != 1 || statusResponse.Headers["Content-Type"][0] != "application/json; charset=utf-8" || status.Name != "cpa-workbuddy" || status.Version != workbuddy.Version() || status.ABIVersion != 1 || status.SchemaVersion != 6 {
		t.Fatalf("unexpected status: %+v, body %s", statusResponse, statusResponse.Body)
	}
	for range 2 {
		if string(call("plugin.shutdown", `{}`)["ok"]) != "true" {
			t.Fatal("RPC shutdown must be idempotent")
		}
	}
	runtime.Shutdown()
	runtime.Shutdown()
	assertError("plugin.register", `{"schema_version":6}`, "plugin_shutdown")
	assertError("management.handle", `{}`, "plugin_shutdown")
}
