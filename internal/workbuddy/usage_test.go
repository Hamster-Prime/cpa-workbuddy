package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestExtractTokenUsageFromPayload(t *testing.T) {
	t.Run("openai json", func(t *testing.T) {
		payload := []byte(`{"id":"chatcmpl-1","usage":{"prompt_tokens":120,"completion_tokens":45,"total_tokens":165,"prompt_tokens_details":{"cached_tokens":20}}}`)
		u, ok := extractTokenUsageFromPayload(payload)
		if !ok {
			t.Fatal("expected usage to be extracted")
		}
		if u.InputTokens != 120 || u.OutputTokens != 45 || u.TotalTokens != 165 || u.CachedTokens != 20 {
			t.Fatalf("unexpected usage: %+v", u)
		}
	})

	t.Run("anthropic json", func(t *testing.T) {
		payload := []byte(`{"type":"message","usage":{"input_tokens":80,"output_tokens":30,"cache_read_input_tokens":10}}`)
		u, ok := extractTokenUsageFromPayload(payload)
		if !ok {
			t.Fatal("expected usage to be extracted")
		}
		if u.InputTokens != 80 || u.OutputTokens != 30 || u.TotalTokens != 110 || u.CachedTokens != 10 {
			t.Fatalf("unexpected usage: %+v", u)
		}
	})

	t.Run("sse stream", func(t *testing.T) {
		payload := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":50,\"completion_tokens\":12,\"total_tokens\":62}}\n\ndata: [DONE]\n\n")
		u, ok := extractTokenUsageFromPayload(payload)
		if !ok {
			t.Fatal("expected usage to be extracted from SSE")
		}
		if u.InputTokens != 50 || u.OutputTokens != 12 || u.TotalTokens != 62 {
			t.Fatalf("unexpected usage: %+v", u)
		}
	})
}

func TestUsageTrackerRecordSnapshotAndReset(t *testing.T) {
	tr := newUsageTracker()
	tr.record(UsageEvent{
		AuthIndex:        "auth-1",
		AccountLabel:     "CN-1",
		Site:             "workbuddy",
		Model:            "deepseek-v3",
		Status:           "success",
		DurationMs:       320,
		CreditFactor:     1.0,
		EstimatedCredits: 1.0,
		Usage:            TokenUsage{InputTokens: 100, OutputTokens: 50},
	})
	tr.record(UsageEvent{
		AuthIndex: "auth-1",
		Site:      "workbuddy",
		Model:     "deepseek-v3",
		Status:    "error",
		Error:     "upstream timeout",
	})

	models, accounts, events := tr.snapshot()
	if len(models) != 1 {
		t.Fatalf("expected 1 model summary, got %d", len(models))
	}
	if models[0].Requests != 2 || models[0].SuccessCount != 1 || models[0].ErrorCount != 1 || models[0].TotalTokens != 150 {
		t.Fatalf("unexpected model summary: %+v", models[0])
	}
	if acc, ok := accounts["auth-1"]; !ok || acc.Requests != 2 || acc.TotalTokens != 150 {
		t.Fatalf("unexpected account summary: %+v", acc)
	}
	if len(events) != 2 || events[0].Status != "error" {
		t.Fatalf("expected newest-first events, got %+v", events)
	}

	tr.reset()
	models, accounts, events = tr.snapshot()
	if len(models) != 0 || len(accounts) != 0 || len(events) != 0 {
		t.Fatalf("expected empty snapshot after reset, got models=%d accounts=%d events=%d", len(models), len(accounts), len(events))
	}
}

func TestHandleManagementUsageEndpoints(t *testing.T) {
	svc := New()
	defer svc.Close()

	svc.usage.record(UsageEvent{
		AuthIndex: "auth-1",
		Site:      "workbuddy",
		Model:     "glm-4.7",
		Status:    "success",
		Usage:     TokenUsage{InputTokens: 40, OutputTokens: 20, TotalTokens: 60},
	})

	call := func(method string, _ []byte) ([]byte, error) {
		if method == "host.auth.list" {
			return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"files": []any{}}})
		}
		return []byte("{}"), nil
	}

	res, err := svc.HandleManagement(context.Background(), "cb-1", call, http.MethodGet, "/v0/management/plugins/cpa-workbuddy/usage", nil, nil, "0.1.2")
	if err != nil {
		t.Fatalf("GET /usage failed: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var stats UsageStatisticsResponse
	if err := json.Unmarshal(res.Body, &stats); err != nil {
		t.Fatalf("unmarshal usage response: %v", err)
	}
	if stats.Overview.TotalRequests != 1 || stats.Overview.TotalTokens != 60 {
		t.Fatalf("unexpected overview: %+v", stats.Overview)
	}

	resetRes, err := svc.HandleManagement(context.Background(), "cb-1", call, http.MethodPost, "/v0/management/plugins/cpa-workbuddy/usage/reset", nil, []byte("{}"), "0.1.2")
	if err != nil {
		t.Fatalf("POST /usage/reset failed: %v", err)
	}
	if resetRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on reset, got %d", resetRes.StatusCode)
	}
	models, _, _ := svc.usage.snapshot()
	if len(models) != 0 {
		t.Fatalf("expected usage to be cleared after reset, got %d models", len(models))
	}
}
