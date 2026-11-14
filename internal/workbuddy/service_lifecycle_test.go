package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

func lifecycleFixture(t *testing.T, method string) (*workbuddy.Service, []byte, hostCall) {
	t.Helper()
	s := workbuddy.New()
	var req any
	var call hostCall
	switch method {
	case "model.for_auth", "auth.refresh":
		auth := refreshIdentity(t, "workbuddy", "", time.Hour)
		req = refreshWire(auth)
		call = newRotationHost(t, auth).call
	case "auth.login.start", "auth.login.poll":
		call = host(t, func(req httpRequest) ([]byte, error) {
			switch {
			case strings.Contains(req.URL, "/v2/plugin/auth/state"):
				return success(stateReply("workbuddy"))
			case strings.Contains(req.URL, "/v2/plugin/auth/token"):
				return success(tokenReply())
			case strings.Contains(req.URL, "/v2/plugin/login/account"):
				return success(map[string]any{"uid": "person", "nickname": "Person", "type": "personal"})
			default:
				return nil, errors.New("unexpected login request")
			}
		})
		req = map[string]any{"Provider": "workbuddy", "Metadata": map[string]any{"site": "workbuddy"}, "host_callback_id": "lifecycle-login"}
		if method == "auth.login.poll" {
			flow := begin(t, s, "workbuddy", call)
			req = map[string]any{"Provider": "workbuddy", "State": flow.State, "host_callback_id": "lifecycle-poll"}
		}
	default:
		t.Fatalf("unsupported lifecycle fixture %s", method)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return s, raw, call
}

func lifecycleCallbackMatches(method string, raw []byte, target string) bool {
	if method == target {
		return true
	}
	if method != "host.http.do" {
		return false
	}
	var req httpRequest
	return json.Unmarshal(raw, &req) == nil && strings.HasSuffix(req.URL, target)
}

func requireServiceClosed(t *testing.T, err error) {
	t.Helper()
	var detail *workbuddy.Error
	if !errors.As(err, &detail) || detail.Code != "service_closed" || detail.HTTPStatus != 503 {
		t.Fatalf("closed service error = %v; want service_closed/503", err)
	}
}

// Observe shutdown through the public admission boundary before releasing a
// blocked callback or starting concurrent Close callers.
func awaitServiceClosed(t *testing.T, s *workbuddy.Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := s.Handle(context.Background(), "auth.identifier", []byte("{}"), nil)
		if err != nil {
			requireServiceClosed(t, err)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Close did not stop service admission")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestServiceCloseWaitsForSynchronousHostCallbacks(t *testing.T) {
	for _, tc := range []struct {
		method string
		target string
	}{
		{"model.for_auth", "/v3/config"},
		{"model.for_auth", "host.log"},
		{"auth.refresh", "/v2/plugin/auth/token/refresh"},
		{"auth.refresh", "/v3/config"},
		{"auth.login.start", "/v2/plugin/auth/state?platform=workbuddy"},
		{"auth.login.poll", "/v2/plugin/auth/token?state=upstream-workbuddy"},
		{"auth.login.poll", "/v2/plugin/login/account?state=upstream-workbuddy"},
	} {
		t.Run(tc.method+"/"+tc.target, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, raw, delegate := lifecycleFixture(t, tc.method)
				defer s.Close()
				entered := make(chan struct{})
				release := make(chan struct{})
				var releaseOnce sync.Once
				defer releaseOnce.Do(func() { close(release) })
				var shutdownReturned atomic.Bool
				var callbacks atomic.Int64
				var active atomic.Int64
				call := func(method string, raw []byte) ([]byte, error) {
					if shutdownReturned.Load() {
						t.Error("host callback ran after Close returned")
					}
					callbacks.Add(1)
					active.Add(1)
					defer active.Add(-1)
					if lifecycleCallbackMatches(method, raw, tc.target) {
						close(entered)
						<-release
					}
					return delegate(method, raw)
				}
				handled := make(chan struct{})
				go func() {
					defer close(handled)
					_, _ = s.Handle(context.Background(), tc.method, raw, call)
				}()
				<-entered
				closed := make(chan struct{}, 8)
				go func() {
					s.Close()
					shutdownReturned.Store(true)
					closed <- struct{}{}
				}()
				awaitServiceClosed(t, s)
				started := make(chan struct{}, cap(closed)-1)
				for range cap(started) {
					go func() {
						started <- struct{}{}
						s.Close()
						shutdownReturned.Store(true)
						closed <- struct{}{}
					}()
				}
				for range cap(started) {
					<-started
				}
				if len(closed) != 0 || active.Load() != 1 {
					t.Fatal("concurrent Close returned while a synchronous host callback was active")
				}
				before := callbacks.Load()
				_, err := s.Handle(context.Background(), tc.method, raw, call)
				requireServiceClosed(t, err)
				if callbacks.Load() != before {
					t.Fatal("closing service admitted another host callback")
				}
				releaseOnce.Do(func() { close(release) })
				<-handled
				for range cap(closed) {
					<-closed
				}
				if active.Load() != 0 {
					t.Fatal("Close returned with an active host callback")
				}
				before = callbacks.Load()
				for _, method := range []string{
					"auth.identifier", "auth.parse", "auth.refresh", "auth.login.start", "auth.login.poll",
					"model.static", "model.for_auth", "executor.identifier", "executor.execute", "executor.execute_stream",
					"executor.count_tokens", "executor.http_request",
				} {
					_, err := s.Handle(context.Background(), method, raw, call)
					requireServiceClosed(t, err)
				}
				time.Sleep(6 * time.Minute)
				synctest.Wait()
				if callbacks.Load() != before {
					t.Fatal("closed service or an expired login timer invoked the host")
				}
				s.Close()
			})
		})
	}
}

func TestServiceLifecyclePreservesCallerCancellation(t *testing.T) {
	for _, method := range []string{"model.for_auth", "auth.refresh", "auth.login.start", "auth.login.poll"} {
		for _, timeout := range []bool{false, true} {
			name := method + "/cancel"
			if timeout {
				name = method + "/deadline"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s, raw, delegate := lifecycleFixture(t, method)
					defer s.Close()
					ctx, cancel := context.WithCancel(context.Background())
					if timeout {
						cancel()
						ctx, cancel = context.WithTimeout(context.Background(), time.Second)
					}
					defer cancel()
					entered := make(chan struct{})
					release := make(chan struct{})
					var releaseOnce sync.Once
					defer releaseOnce.Do(func() { close(release) })
					call := func(callback string, raw []byte) ([]byte, error) {
						if callback == "host.http.do" {
							close(entered)
							<-release
						}
						return delegate(callback, raw)
					}
					type outcome struct {
						value any
						err   error
					}
					result := make(chan outcome, 1)
					go func() {
						value, err := s.Handle(ctx, method, raw, call)
						result <- outcome{value, err}
					}()
					<-entered
					if timeout {
						time.Sleep(time.Second)
					} else {
						cancel()
					}
					synctest.Wait()
					releaseOnce.Do(func() { close(release) })
					got := <-result
					if method == "auth.login.poll" {
						value, ok := got.value.(workbuddy.PollResponse)
						if got.err != nil || !ok || value.Status != "error" || value.Auth.Provider != "" {
							t.Fatalf("cancelled poll = %#v, %v; want session error without credentials", got.value, got.err)
						}
					} else {
						var detail *workbuddy.Error
						if !errors.As(got.err, &detail) || detail.HTTPStatus != 408 {
							t.Fatalf("cancelled %s = %v; want cancellation/408", method, got.err)
						}
					}
					if _, err := s.Handle(context.Background(), "auth.identifier", []byte("{}"), nil); err != nil {
						t.Fatalf("caller cancellation closed the service: %v", err)
					}
					if method == "auth.login.poll" {
						value, err := s.Handle(context.Background(), method, raw, func(string, []byte) ([]byte, error) {
							t.Error("cancelled login session invoked the host again")
							return nil, errors.New("unexpected callback")
						})
						if err != nil || value.(workbuddy.PollResponse).Status != "error" {
							t.Fatal("caller cancellation did not discard the login session")
						}
					}
				})
			})
		}
	}
}

func TestServiceCloseRacesSynchronousAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, raw, delegate := lifecycleFixture(t, "auth.login.start")
		defer s.Close()
		start := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		var closed atomic.Bool
		var active atomic.Int64
		call := func(method string, raw []byte) ([]byte, error) {
			if closed.Load() {
				t.Error("racing Handle invoked the host after Close returned")
			}
			active.Add(1)
			defer active.Add(-1)
			<-release
			return delegate(method, raw)
		}
		results := make(chan error, 32)
		for range cap(results) {
			go func() {
				<-start
				_, err := s.Handle(context.Background(), "auth.login.start", raw, call)
				results <- err
			}()
		}
		done := make(chan struct{})
		go func() {
			<-start
			s.Close()
			closed.Store(true)
			close(done)
		}()
		close(start)
		awaitServiceClosed(t, s)
		if closed.Load() && active.Load() != 0 {
			t.Fatal("Close missed synchronous work racing admission")
		}
		releaseOnce.Do(func() { close(release) })
		for range cap(results) {
			err := <-results
			var detail *workbuddy.Error
			if !errors.As(err, &detail) || (detail.Code != "service_closed" && detail.Code != "login_cancelled") {
				t.Fatalf("racing login error = %v; want closed or cancelled", err)
			}
		}
		<-done
		if active.Load() != 0 {
			t.Fatal("racing Close returned before callbacks finished")
		}
	})
}
