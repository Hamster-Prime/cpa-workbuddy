package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

func chatWireRequest(auth workbuddy.AuthData, streaming bool) map[string]any {
	prefix := "wb/"
	if auth.Attributes["site"] == "workbuddy-ai" {
		prefix = "wb-ai/"
	}
	return map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON, "Model": prefix + "common", "Format": "openai", "Stream": streaming, "Payload": []byte(`{"model":"client-alias","messages":[{"role":"user","content":"hello"}]}`), "host_callback_id": "chat-context", "stream_id": "downstream"}
}

func TestChatHTTPFailuresCloseOwnedUpstream(t *testing.T) {
	for _, mode := range []string{"version", "transport", "bad-open", "read-error", "close-error"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			closed := 0
			read := 0
			_, err := invoke(s, context.Background(), "executor.execute", chatWireRequest(parsedIdentity(t, "workbuddy", ""), false), func(method string, _ []byte) ([]byte, error) {
				switch method {
				case "host.http.do_stream":
					if mode == "transport" {
						return nil, errors.New("secret transport details")
					}
					if mode == "bad-open" {
						return []byte(`{"ok":true,"result":{"status_code":200}}`), nil
					}
					status := 200
					if mode == "version" {
						status = 400
					}
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"status_code": status, "stream_id": "upstream"}})
				case "host.http.stream_read":
					read++
					if mode == "read-error" {
						return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"error": "private token response", "done": true}})
					}
					data := []byte("data: {\"id\":\"a\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					if mode == "version" {
						data = []byte(`{"code":10085,"msg":"secret upstream message"}`)
					}
					return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": data, "done": true}})
				case "host.http.stream_close":
					closed++
					if mode == "close-error" {
						return nil, errors.New("private close error")
					}
					return []byte(`{"ok":true,"result":{}}`), nil
				default:
					t.Fatalf("unexpected callback %s", method)
					return nil, nil
				}
			})
			if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") {
				t.Fatalf("failure result leaked or disappeared: %v", err)
			}
			wantClosed := 1
			if mode == "transport" || mode == "bad-open" {
				wantClosed = 0
			}
			if closed != wantClosed {
				t.Fatalf("upstream close count=%d", closed)
			}
			if mode == "version" {
				detail, ok := errors.AsType[*workbuddy.Error](err)
				if !ok || detail.Code != "client_version_rejected" {
					t.Fatalf("version diagnostic=%v", err)
				}
			}
			if mode == "read-error" && read != 1 {
				t.Fatal("failed stream was replayed")
			}
		})
	}
}

func TestChatCancellationAndServiceCloseUnblockInFlightReads(t *testing.T) {
	for _, mode := range []string{"caller", "service"} {
		t.Run(mode, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reading := make(chan struct{})
			upClosed := make(chan struct{})
			downClosed := make(chan string, 1)
			var once sync.Once
			_, err := invoke(s, ctx, "executor.execute_stream", chatWireRequest(parsedIdentity(t, "workbuddy-ai", "team"), true), func(method string, _ []byte) ([]byte, error) {
				switch method {
				case "host.http.do_stream":
					return []byte(`{"ok":true,"result":{"status_code":200,"stream_id":"upstream"}}`), nil
				case "host.http.stream_read":
					close(reading)
					<-upClosed
					return []byte(`{"ok":true,"result":{"done":true}}`), nil
				case "host.http.stream_close":
					once.Do(func() { close(upClosed) })
					return []byte(`{"ok":true,"result":{}}`), nil
				case "host.stream.close":
					downClosed <- "closed"
					return []byte(`{"ok":true,"result":{}}`), nil
				default:
					return nil, errors.New("unexpected callback")
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-reading:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream read did not begin")
			}
			if mode == "caller" {
				cancel()
			} else {
				s.Close()
			}
			select {
			case <-downClosed:
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled downstream did not close")
			}
			s.Close()
			select {
			case <-downClosed:
				t.Fatal("downstream closed more than once")
			default:
			}
		})
	}
}

func TestChatCancellationInterruptsBlockedEmission(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitting := make(chan struct{})
	downstreamClosed := make(chan struct{})
	var once sync.Once
	_, err := invoke(s, ctx, "executor.execute_stream", chatWireRequest(parsedIdentity(t, "workbuddy", ""), true), func(method string, _ []byte) ([]byte, error) {
		switch method {
		case "host.http.do_stream":
			return []byte(`{"ok":true,"result":{"status_code":200,"stream_id":"upstream"}}`), nil
		case "host.http.stream_read":
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": []byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), "done": true}})
		case "host.stream.emit":
			close(emitting)
			<-downstreamClosed
			return nil, errors.New("stream closed while emitter waited")
		case "host.stream.close":
			once.Do(func() { close(downstreamClosed) })
			return []byte(`{"ok":true,"result":{}}`), nil
		case "host.http.stream_close":
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			return nil, errors.New("unexpected callback")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-emitting:
	case <-time.After(3 * time.Second):
		t.Fatal("emission did not block")
	}
	cancel()
	joined := make(chan struct{})
	go func() { s.Close(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation deadlocked with blocked emission")
	}
}

func TestChatCloseJoinsHostCleanupDiagnostics(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	diagnostic := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	_, err := invoke(s, context.Background(), "executor.execute_stream", chatWireRequest(parsedIdentity(t, "workbuddy", ""), true), func(method string, raw []byte) ([]byte, error) {
		switch method {
		case "host.http.do_stream":
			return []byte(`{"ok":true,"result":{"status_code":200,"stream_id":"upstream"}}`), nil
		case "host.http.stream_read":
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"payload": []byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), "done": true}})
		case "host.stream.close":
			return nil, errors.New("private close failure")
		case "host.log":
			var req struct {
				CallbackID string `json:"host_callback_id"`
				Message    string `json:"message"`
			}
			if json.Unmarshal(raw, &req) != nil || req.CallbackID != "chat-context" || req.Message != "WorkBuddy stream cleanup failed" || strings.Contains(string(raw), "private") {
				t.Error("host cleanup diagnostic lost context or leaked private details")
			}
			once.Do(func() { close(diagnostic) })
			<-release
			return []byte(`{"ok":true,"result":{}}`), nil
		case "host.stream.emit", "host.http.stream_close":
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			return nil, errors.New("unexpected callback")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-diagnostic:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup failure was not sent through host.log")
	}
	joined := make(chan struct{})
	go func() { s.Close(); close(joined) }()
	select {
	case <-joined:
		t.Error("Close returned while host diagnostic still used callback")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not join cleanup diagnostics")
	}
}
