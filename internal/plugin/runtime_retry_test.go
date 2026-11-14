package plugin_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"cpa-workbuddy/internal/plugin"
)

func TestRuntimeExportsNativeRetryAfterSeconds(t *testing.T) {
	for _, test := range []struct {
		name, header     string
		minimum, maximum int
	}{
		{"seconds", "17", 17, 17},
		{"padded_seconds", "00017", 17, 17},
		{"elapsed_date", "Wed, 21 Oct 2015 07:28:00 GMT", 0, 0},
		{"future_date", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), 3500, 3600},
	} {
		t.Run(test.name, func(t *testing.T) {
			closes := 0
			runtime := plugin.NewWithHost(func(method string, _ []byte) ([]byte, error) {
				switch method {
				case "host.http.do_stream":
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": 429, "headers": map[string][]string{"Retry-After": {test.header}}, "stream_id": "rate-limit"}})
				case "host.http.stream_read":
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": []byte(`{"error":{"message":"capacity"}}`), "done": true}})
				case "host.http.stream_close":
					closes++
					return []byte(`{"ok":true,"result":{}}`), nil
				default:
					return nil, errors.New("unexpected rate-limit callback")
				}
			})
			defer runtime.Shutdown()
			var registered struct {
				OK bool `json:"ok"`
			}
			if err := json.Unmarshal(runtime.Handle("plugin.register", []byte(`{"schema_version":10}`)), &registered); err != nil || !registered.OK {
				t.Fatal("registration failed")
			}
			credential := []byte(`{"provider":"workbuddy","type":"workbuddy","credential_type":"browser","version":1,"site":"workbuddy","uid":"rate-limit","access_token":"fixture-access","expires_at":"2030-01-01T00:00:00Z"}`)
			raw, err := json.Marshal(map[string]any{"Provider": "workbuddy", "RawJSON": credential})
			if err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				Result struct {
					Auth struct {
						ID          string
						StorageJSON []byte
					}
				}
			}
			if err := json.Unmarshal(runtime.Handle("auth.parse", raw), &parsed); err != nil || parsed.Result.Auth.ID == "" {
				t.Fatal("credential parse failed")
			}
			raw, err = json.Marshal(map[string]any{"AuthID": parsed.Result.Auth.ID, "AuthProvider": "workbuddy", "StorageJSON": parsed.Result.Auth.StorageJSON, "Model": "wb/common", "Format": "openai", "Payload": []byte(`{"messages":[{"role":"user","content":"hello"}]}`)})
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				OK    bool `json:"ok"`
				Error struct {
					Code              string `json:"code"`
					HTTPStatus        int    `json:"http_status"`
					RetryAfterSeconds *int   `json:"retry_after_seconds"`
				} `json:"error"`
			}
			if err := json.Unmarshal(runtime.Handle("executor.execute", raw), &response); err != nil {
				t.Fatal(err)
			}
			delay := response.Error.RetryAfterSeconds
			if response.OK || response.Error.Code != "rate_limit_exceeded" || response.Error.HTTPStatus != 429 || delay == nil || *delay < test.minimum || *delay > test.maximum || closes != 1 {
				t.Fatalf("rate limit lost native retry guidance or stream release: %+v closes=%d", response, closes)
			}
		})
	}
}
