package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"cpa-workbuddy/internal/workbuddy"
)

var errHostGetFailed = errors.New("host auth get failed")

const transferPreviewPath = "/v0/management/plugins/cpa-workbuddy/transfer/preview"
const transferExportPath = "/v0/management/plugins/cpa-workbuddy/transfer/export"
const transferImportPath = "/v0/management/plugins/cpa-workbuddy/transfer/import"

func postManagement(t *testing.T, s *workbuddy.Service, h *fakeHostHarness, path, body string) (workbuddy.ManagementResult, error) {
	t.Helper()
	return s.HandleManagement(context.Background(), "cb-transfer", h.call, http.MethodPost, path, url.Values{}, []byte(body), "0.2.4")
}

func TestTransferRoutesPreviewExportImport(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()

	cnAuth := parsedIdentity(t, "workbuddy", "")
	h := newFakeHostHarness(t, []workbuddy.AuthData{cnAuth})

	// 1. preview: redacted, reports importability, never echoes the token.
	const token = "route-preview-secret"
	previewRes, err := postManagement(t, s, h, transferPreviewPath,
		`{"text":"{\"accounts\":[{\"access_token\":\"`+token+`\",\"uid\":\"uid-route\",\"nickname\":\"路线账号\",\"domain\":\"copilot.tencent.com\"}]}"}`)
	if err != nil || previewRes.StatusCode != 200 {
		t.Fatalf("unexpected /transfer/preview response: status=%d err=%v", previewRes.StatusCode, err)
	}
	if strings.Contains(string(previewRes.Body), token) {
		t.Fatalf("preview leaked the access token: %s", previewRes.Body)
	}
	var preview struct {
		Accounts []struct {
			Index      int    `json:"index"`
			Nickname   string `json:"nickname"`
			Site       string `json:"site"`
			HasToken   bool   `json:"has_token"`
			Importable bool   `json:"importable"`
		} `json:"accounts"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(previewRes.Body, &preview); err != nil {
		t.Fatalf("invalid preview payload: %s err=%v", previewRes.Body, err)
	}
	if preview.Total != 1 || len(preview.Accounts) != 1 || !preview.Accounts[0].Importable ||
		!preview.Accounts[0].HasToken || preview.Accounts[0].Site != "workbuddy" {
		t.Fatalf("unexpected preview payload: %s", previewRes.Body)
	}

	// 2. import: reports duplicated separately and stores exactly one credential.
	importRes, err := postManagement(t, s, h, transferImportPath,
		`{"text":"[{\"access_token\":\"first\",\"uid\":\"uid-dup\",\"domain\":\"copilot.tencent.com\"},{\"access_token\":\"second\",\"uid\":\"uid-dup\",\"domain\":\"copilot.tencent.com\"}]","indexes":[0,1]}`)
	if err != nil || importRes.StatusCode != 200 {
		t.Fatalf("unexpected /transfer/import response: status=%d err=%v", importRes.StatusCode, err)
	}
	var imported struct {
		Imported   int `json:"imported"`
		Skipped    int `json:"skipped"`
		Duplicated int `json:"duplicated"`
	}
	if err := json.Unmarshal(importRes.Body, &imported); err != nil {
		t.Fatalf("invalid import payload: %s err=%v", importRes.Body, err)
	}
	if imported.Imported != 1 || imported.Skipped != 0 || imported.Duplicated != 1 {
		t.Fatalf("unexpected import counts: %s", importRes.Body)
	}

	// 3. export: the stored credential round-trips through the reference envelope.
	exportRes, err := postManagement(t, s, h, transferExportPath, `{"auth_indexes":["workbuddy-a"]}`)
	if err != nil || exportRes.StatusCode != 200 {
		t.Fatalf("unexpected /transfer/export response: status=%d err=%v", exportRes.StatusCode, err)
	}
	var exported struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(exportRes.Body, &exported); err != nil {
		t.Fatalf("invalid export payload: %s err=%v", exportRes.Body, err)
	}
	if len(exported.Accounts) != 1 {
		t.Fatalf("expected one exported account: %s", exportRes.Body)
	}
	if exported.Accounts[0]["auth_raw"] == nil {
		t.Fatalf("export lost auth_raw: %s", exportRes.Body)
	}
	if _, ok := exported.Accounts[0]["expiresAt"]; !ok {
		t.Fatalf("export lost expiresAt: %s", exportRes.Body)
	}

	// The exported body must be re-importable without changes.
	reimport := map[string]any{"accounts": exported.Accounts}
	rawReimport, err := json.Marshal(reimport)
	if err != nil {
		t.Fatal(err)
	}
	reimportRes, err := postManagement(t, s, h, transferPreviewPath,
		`{"text":`+string(mustJSONQuote(t, string(rawReimport)))+`}`)
	if err != nil || reimportRes.StatusCode != 200 {
		t.Fatalf("exported file was rejected: status=%d err=%v", reimportRes.StatusCode, err)
	}
	if !strings.Contains(string(reimportRes.Body), `"importable":true`) {
		t.Fatalf("exported file is not importable: %s", reimportRes.Body)
	}
}

func TestTransferImportIsolatesBadRecordsAndHostReadFailures(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()

	cnAuth := parsedIdentity(t, "workbuddy", "")
	h := newFakeHostHarness(t, []workbuddy.AuthData{cnAuth})
	// One valid record, one entry that is not an object, one with an unknown
	// domain: the batch must import the valid one and skip the other two.
	res, err := postManagement(t, s, h, transferImportPath,
		`{"text":"[{\"access_token\":\"good\",\"uid\":\"uid-good\",\"domain\":\"copilot.tencent.com\"},\"not-an-object\",{\"access_token\":\"x\",\"uid\":\"uid-bad\",\"domain\":\"unknown.example\"}]","indexes":[0,1,2]}`)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("unexpected import response: status=%d err=%v", res.StatusCode, err)
	}
	var counts struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
	}
	if err := json.Unmarshal(res.Body, &counts); err != nil {
		t.Fatalf("invalid payload: %s err=%v", res.Body, err)
	}
	if counts.Imported != 1 || counts.Skipped != 2 {
		t.Fatalf("bad records were not isolated: %s", res.Body)
	}

	// A failing host read must surface as an error, not be silently skipped.
	failing := newFakeHostHarness(t, []workbuddy.AuthData{cnAuth})
	failing.callOverride = func(method string) ([]byte, error, bool) {
		if method == "host.auth.get" {
			return nil, errHostGetFailed, true
		}
		return nil, nil, false
	}
	_, err = postManagement(t, s, failing, transferImportPath,
		`{"text":"[{\"access_token\":\"new\",\"uid\":\"uid-new\",\"domain\":\"copilot.tencent.com\"}]","indexes":[0]}`)
	if err == nil {
		t.Fatal("expected host read failure to surface as an error")
	}
}

func TestTransferExportRejectsUnknownSelection(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	cnAuth := parsedIdentity(t, "workbuddy", "")
	h := newFakeHostHarness(t, []workbuddy.AuthData{cnAuth})

	res, err := postManagement(t, s, h, transferExportPath, `{"auth_indexes":["missing"]}`)
	if err == nil && res.StatusCode == 200 {
		t.Fatalf("expected an error for an unknown auth_index, got %d", res.StatusCode)
	}
	if err == nil {
		t.Fatalf("expected a protocol error, got body %s", res.Body)
	}

	res, err = postManagement(t, s, h, transferExportPath, `{"auth_indexes":[]}`)
	if err == nil && res.StatusCode == 200 {
		t.Fatal("expected an error for an empty selection")
	}
}

func mustJSONQuote(t *testing.T, raw string) string {
	t.Helper()
	quoted, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(quoted)
}
