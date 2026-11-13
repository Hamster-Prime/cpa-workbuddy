package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"maps"
	"sync"
	"time"
)

// ExecutorResponse follows CPA's non-streaming executor response schema.
type ExecutorResponse struct {
	Payload  []byte
	Headers  map[string][]string
	Metadata map[string]any
}

type executorStreamResponse struct {
	Headers map[string][]string `json:"headers"`
}

type streamReference struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

type chatLease struct {
	call       func(string, []byte) ([]byte, error)
	upstream   string
	downstream string
	callbackID string
	upOnce     sync.Once
	upErr      error
	downOnce   sync.Once
	downErr    error
}

func hostRPC(call func(string, []byte) ([]byte, error), method string, req, target any) error {
	if call == nil {
		return failure("host_unavailable", "host callback is unavailable", 503)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return failure("internal_error", "cannot encode host callback request", 500)
	}
	reply, err := call(method, raw)
	if err != nil {
		return failure("host_callback_failed", "host callback failed", 502)
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(reply, &envelope) != nil || !envelope.OK || (target != nil && json.Unmarshal(envelope.Result, target) != nil) {
		return failure("host_callback_failed", "host returned an invalid callback response", 502)
	}
	return nil
}

func (lease *chatLease) closeUpstream() error {
	lease.upOnce.Do(func() {
		lease.upErr = hostRPC(lease.call, "host.http.stream_close", streamReference{StreamID: lease.upstream}, nil)
	})
	return lease.upErr
}

func (lease *chatLease) closeDownstream(err error) error {
	if lease.downstream == "" {
		return nil
	}
	lease.downOnce.Do(func() {
		ref := streamReference{StreamID: lease.downstream}
		if err != nil {
			ref.Error = err.Error()
		}
		lease.downErr = hostRPC(lease.call, "host.stream.close", ref, nil)
	})
	return lease.downErr
}

func (lease *chatLease) reportCleanup(stage string, cleanupErr error) {
	request := struct {
		HostCallbackID string            `json:"host_callback_id"`
		Level          string            `json:"level"`
		Message        string            `json:"message"`
		Fields         map[string]string `json:"fields"`
	}{lease.callbackID, "error", "WorkBuddy stream cleanup failed", map[string]string{"stage": stage, "error": cleanupErr.Error()}}
	if err := hostRPC(lease.call, "host.log", request, nil); err != nil {
		// The host diagnostic channel failed too; stderr is the last available sink.
		log.Print("WorkBuddy stream diagnostic callback failed: ", err, "; cleanup: ", cleanupErr)
	}
}

func (lease *chatLease) read(ctx context.Context) ([]byte, bool, error) {
	if ctx.Err() != nil {
		return nil, true, failure("request_cancelled", "WorkBuddy request was cancelled or expired", 408)
	}
	var chunk struct {
		Payload []byte `json:"payload"`
		Error   string `json:"error"`
		Done    bool   `json:"done"`
	}
	if err := hostRPC(lease.call, "host.http.stream_read", streamReference{StreamID: lease.upstream}, &chunk); err != nil {
		return nil, true, err
	}
	if ctx.Err() != nil {
		return nil, true, failure("request_cancelled", "WorkBuddy request was cancelled or expired", 408)
	}
	if chunk.Error != "" {
		return nil, true, failure("upstream_stream_failed", "WorkBuddy response stream failed", 502)
	}
	return chunk.Payload, chunk.Done, nil
}

func (s *Service) execute(ctx context.Context, method string, raw []byte, call func(string, []byte) ([]byte, error)) (any, error) {
	if method == "executor.identifier" {
		var req struct{}
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		return map[string]string{"identifier": provider}, nil
	}
	if method == "executor.count_tokens" || method == "executor.http_request" {
		return nil, failure("unsupported_operation", "WorkBuddy provides model execution through its Chat endpoint", 501)
	}
	var req executorRequest
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	streaming := method == "executor.execute_stream"
	if streaming && req.StreamID == "" {
		return nil, failure("invalid_request", "streaming execution requires a host stream ID", 400)
	}
	prepared, err := prepareChat(req)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, failure("service_closed", "WorkBuddy service is closed", 503)
	}
	s.jobs.Add(1)
	serviceCtx := s.execCtx
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 1200*time.Second)
	stopService := context.AfterFunc(serviceCtx, cancel)
	finish := func() { stopService(); cancel(); s.jobs.Done() }
	auth, err := checkedAuth(req.AuthID, req.AuthProvider, req.StorageJSON, req.AuthMetadata)
	if err != nil {
		finish()
		return nil, err
	}
	auth, authRecovered, err := s.freshAuth(ctx, auth, req.HostCallbackID, call)
	if err != nil {
		finish()
		return nil, err
	}
	applyAuth := func(next AuthData) error {
		headers, err := next.RequestHeaders()
		if err != nil {
			return err
		}
		maps.Copy(prepared.request.Headers, headers)
		return nil
	}
	if err := applyAuth(auth); err != nil {
		finish()
		return nil, err
	}
	for {
		var opened struct {
			StatusCode int                 `json:"status_code"`
			Headers    map[string][]string `json:"headers"`
			StreamID   string              `json:"stream_id"`
		}
		if err := hostRPC(call, "host.http.do_stream", prepared.request, &opened); err != nil {
			finish()
			return nil, err
		}
		if opened.StreamID == "" {
			finish()
			return nil, failure("host_callback_failed", "host returned no upstream stream ID", 502)
		}
		lease := &chatLease{call: call, upstream: opened.StreamID, callbackID: req.HostCallbackID}
		if streaming {
			lease.downstream = req.StreamID
		}
		cancelled := make(chan struct{})
		stopCancel := context.AfterFunc(ctx, func() {
			defer close(cancelled)
			cancelErr := failure("request_cancelled", "WorkBuddy request was cancelled or expired", 408)
			if err := lease.closeUpstream(); err != nil {
				lease.reportCleanup("upstream", err)
			}
			if err := lease.closeDownstream(cancelErr); err != nil {
				lease.reportCleanup("downstream", err)
			}
		})
		cleanup := func() error {
			upErr := lease.closeUpstream()
			if !stopCancel() {
				<-cancelled
			}
			return upErr
		}
		if opened.StatusCode < 200 || opened.StatusCode >= 300 {
			// Error contents stay bounded and private, including the first 401.
			var body []byte
			var readErr error
			for len(body) < 64*1024 {
				chunk, done, err := lease.read(ctx)
				if err != nil {
					readErr = err
					break
				}
				remaining := 64*1024 - len(body)
				if len(chunk) > remaining {
					chunk = chunk[:remaining]
				}
				body = append(body, chunk...)
				if done {
					break
				}
			}
			closeErr := cleanup()
			if readErr != nil {
				finish()
				return nil, errors.Join(chatHTTPError(opened.StatusCode, body), readErr, closeErr)
			}
			if closeErr != nil {
				lease.reportCleanup("upstream", closeErr)
			}
			if opened.StatusCode == 401 && !authRecovered {
				authRecovered = true
				next, err := s.recoverAuthentication(ctx, auth, authenticationReason(opened.StatusCode, opened.Headers, body), req.HostCallbackID, call)
				if err != nil {
					finish()
					return nil, errors.Join(err, closeErr)
				}
				if err := applyAuth(next); err != nil {
					finish()
					return nil, errors.Join(err, closeErr)
				}
				auth = next
				continue
			}
			if reason := authenticationReason(opened.StatusCode, opened.Headers, body); reason != "" {
				_, stateErr := s.invalidate(ctx, auth, reason, req.HostCallbackID, call)
				finish()
				if stateErr == nil {
					return nil, failure("credentials_changed", "WorkBuddy credentials changed during this request; retry with the installed credentials", 409)
				}
				return nil, errors.Join(stateErr, closeErr)
			}
			finish()
			return nil, errors.Join(chatHTTPError(opened.StatusCode, body), closeErr)
		}
		if ctx.Err() != nil {
			closeErr := cleanup()
			finish()
			return nil, errors.Join(failure("request_cancelled", "WorkBuddy request was cancelled or expired", 408), closeErr)
		}
		read := func() ([]byte, bool, error) { return lease.read(ctx) }
		if !streaming {
			payload, parseErr := consumeChatStream(read, prepared.model, prepared.toolNames, nil)
			closeErr := cleanup()
			finish()
			if parseErr != nil || closeErr != nil {
				return nil, errors.Join(parseErr, closeErr)
			}
			return ExecutorResponse{Payload: payload, Headers: map[string][]string{"Content-Type": {"application/json"}}}, nil
		}
		go func() {
			defer finish()
			_, streamErr := consumeChatStream(read, prepared.model, prepared.toolNames, func(payload []byte) error {
				if ctx.Err() != nil {
					return failure("request_cancelled", "WorkBuddy request was cancelled or expired", 408)
				}
				return hostRPC(call, "host.stream.emit", struct {
					StreamID string `json:"stream_id"`
					Payload  []byte `json:"payload"`
				}{req.StreamID, payload}, nil)
			})
			streamErr = errors.Join(streamErr, lease.closeUpstream())
			if closeErr := lease.closeDownstream(streamErr); closeErr != nil {
				lease.reportCleanup("downstream", closeErr)
			}
			if closeErr := cleanup(); closeErr != nil {
				lease.reportCleanup("upstream", closeErr)
			}
		}()
		return executorStreamResponse{Headers: map[string][]string{"Content-Type": {"text/event-stream"}}}, nil
	}
}

func chatHTTPError(status int, body []byte) error {
	var response struct {
		Code  json.RawMessage `json:"code"`
		Error struct {
			Code json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) == nil && (string(response.Code) == "10085" || string(response.Code) == `"10085"` || string(response.Error.Code) == "10085" || string(response.Error.Code) == `"10085"`) {
		return failure("client_version_rejected", "WorkBuddy rejected the client version (10085)", 502)
	}
	if status < 400 || status > 599 {
		status = 502
	}
	return failure("upstream_http_failed", "WorkBuddy Chat request failed", status)
}
