package plugin_test

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cpa-workbuddy/internal/plugin"
	"cpa-workbuddy/internal/workbuddy"
)

func TestRuntimeShutdownJoinsStreamingHostCallbacks(t *testing.T) {
	reading := make(chan struct{})
	upClosed := make(chan struct{})
	downClosed := make(chan struct{})
	var upstreamOnce, downstreamOnce sync.Once
	var released atomic.Bool
	var late atomic.Int32
	runtime := plugin.NewWithHost(func(method string, _ []byte) ([]byte, error) {
		if released.Load() {
			late.Add(1)
		}
		switch method {
		case "host.http.do_stream":
			return []byte(`{"ok":true,"result":{"status_code":200,"stream_id":"upstream"}}`), nil
		case "host.http.stream_read":
			close(reading)
			<-upClosed
			return []byte(`{"ok":true,"result":{"done":true}}`), nil
		case "host.http.stream_close":
			upstreamOnce.Do(func() { close(upClosed) })
			return []byte(`{"ok":true,"result":{}}`), nil
		case "host.stream.close":
			downstreamOnce.Do(func() { close(downClosed) })
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			return nil, errors.New("unexpected callback")
		}
	})
	defer runtime.Shutdown()
	var registered struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(runtime.Handle("plugin.register", []byte(`{"schema_version":10}`)), &registered) != nil || !registered.OK {
		t.Fatal("registration failed")
	}
	credential := []byte(`{"provider":"workbuddy","type":"workbuddy","credential_type":"browser","version":1,"site":"workbuddy-ai","uid":"lifecycle","access_token":"fixture-access","expires_at":"2030-01-01T00:00:00Z"}`)
	parseRequest, err := json.Marshal(map[string]any{"Provider": "workbuddy", "RawJSON": credential})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Result workbuddy.ParseResponse `json:"result"`
	}
	if json.Unmarshal(runtime.Handle("auth.parse", parseRequest), &parsed) != nil || !parsed.Result.Handled {
		t.Fatal("credential parse failed")
	}
	request, err := json.Marshal(map[string]any{"AuthID": parsed.Result.Auth.ID, "AuthProvider": "workbuddy", "StorageJSON": parsed.Result.Auth.StorageJSON, "Model": "wb-ai/common", "Format": "openai", "Payload": []byte(`{"messages":[{"role":"user","content":"hello"}]}`), "stream_id": "downstream"})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(runtime.Handle("executor.execute_stream", request), &started) != nil || !started.OK {
		t.Fatal("stream did not start")
	}
	select {
	case <-reading:
	case <-time.After(3 * time.Second):
		t.Fatal("stream read did not begin")
	}
	shutdown := make(chan struct{})
	go func() { runtime.Shutdown(); released.Store(true); close(shutdown) }()
	select {
	case <-shutdown:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not close stream and join callbacks")
	}
	select {
	case <-downClosed:
	default:
		t.Fatal("shutdown returned before downstream closed")
	}
	if late.Load() != 0 {
		t.Fatal("host callback executed after shutdown released host")
	}
}
