package workbuddy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseTransferFileAcceptsAccountsEnvelope(t *testing.T) {
	got, err := parseTransferFile([]byte(`{"accounts":[{"access_token":"access-a","uid":"uid-a","domain":"copilot.tencent.com"}]}`))
	if err != nil {
		t.Fatalf("parseTransferFile returned error: %v", err)
	}
	if len(got) != 1 || got[0].AccessToken != "access-a" || got[0].UID != "uid-a" || got[0].problem != "" {
		t.Fatalf("unexpected records: %#v", got)
	}
}

func TestParseTransferFileDoesNotRejectFileForOneBadEntry(t *testing.T) {
	// A non-object entry and an entry with an unsafe token must not abort the
	// batch: they become per-record problems that import skips.
	got, err := parseTransferFile([]byte(`[
		{"access_token":"ok-a","uid":"uid-a","domain":"copilot.tencent.com"},
		"not-an-account",
		{"access_token":"bad\u0000token","uid":"uid-b","domain":"copilot.tencent.com"},
		{"access_token":"ok-b","uid":"uid-c","domain":"www.workbuddy.ai"}
	]`))
	if err != nil {
		t.Fatalf("parseTransferFile rejected the whole file: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 records, got %d", len(got))
	}
	if got[0].problem != "" || got[3].problem != "" {
		t.Fatalf("valid records were marked bad: %#v", got)
	}
	if !strings.Contains(got[1].problem, "item 2") || !strings.Contains(got[2].problem, "item 3") {
		t.Fatalf("bad records lost their reason: %#v", got)
	}
}

func TestParseTransferFileRejectsUnusableFiles(t *testing.T) {
	for name, input := range map[string]string{
		"empty":      "",
		"not json":   "{oops",
		"bare value": `42`,
		"no wrapper": `{"unrelated":[]}`,
	} {
		if _, err := parseTransferFile([]byte(input)); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestPreviewTransferFileRedactsTokenAndMarksProblems(t *testing.T) {
	preview, err := previewTransferFile([]byte(`[
		{"access_token":"super-secret","uid":"uid-a","nickname":"账号A","domain":"copilot.tencent.com"},
		{"uid":"uid-b","domain":"copilot.tencent.com"},
		{"access_token":"tok","uid":"uid-c","domain":"unknown.example"}
	]`))
	if err != nil {
		t.Fatalf("previewTransferFile returned error: %v", err)
	}
	if len(preview.Accounts) != 3 || preview.Total != 3 {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	if !preview.Accounts[0].HasToken || !preview.Accounts[0].Importable || preview.Accounts[0].Site != "workbuddy" {
		t.Fatalf("first entry should be importable: %#v", preview.Accounts[0])
	}
	if preview.Accounts[1].Importable || preview.Accounts[1].HasToken {
		t.Fatalf("token-less entry should be flagged: %#v", preview.Accounts[1])
	}
	if preview.Accounts[2].Importable || !strings.Contains(preview.Accounts[2].Problem, "domain") {
		t.Fatalf("unknown domain should be flagged: %#v", preview.Accounts[2])
	}
	if strings.Contains(fmt.Sprintf("%#v", preview), "super-secret") {
		t.Fatal("preview contains access token")
	}
}

func TestMergeTransferRecordsDeduplicatesAndCountsStoredCredentials(t *testing.T) {
	records := []transferRecord{
		{AccessToken: "old", UID: "same", Domain: "copilot.tencent.com"},
		{AccessToken: "new", UID: "same", Domain: "copilot.tencent.com"},
	}
	merged := mergeTransferRecords(records, []int{0, 1})
	if merged.Imported != 1 || merged.Duplicated != 1 || merged.Skipped != 0 || len(merged.Changes) != 1 {
		t.Fatalf("unexpected merge counts: imported=%d duplicated=%d skipped=%d changes=%d",
			merged.Imported, merged.Duplicated, merged.Skipped, len(merged.Changes))
	}
	if merged.Imported != len(merged.Changes) {
		t.Fatal("imported must equal the number of credentials actually stored")
	}
	for _, credential := range merged.Changes {
		if credential.AccessToken != "new" {
			t.Fatalf("duplicate did not use last record: %#v", credential)
		}
	}
}

func TestMergeTransferRecordsSkipsBadEntriesIndividually(t *testing.T) {
	records, err := parseTransferFile([]byte(`[
		{"access_token":"cn","uid":"uid-cn","domain":"copilot.tencent.com"},
		{"access_token":"global","uid":"uid-global","domain":"www.workbuddy.ai"},
		{"access_token":"unknown","uid":"uid-unknown","domain":"unknown.example"},
		{"uid":"uid-no-token","domain":"copilot.tencent.com"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	merged := mergeTransferRecords(records, []int{0, 1, 2, 3, 9})
	if len(merged.Changes) != 2 || merged.Imported != 2 || merged.Skipped != 3 {
		t.Fatalf("unexpected mixed merge counts: changes=%d imported=%d skipped=%d",
			len(merged.Changes), merged.Imported, merged.Skipped)
	}
}

func TestTransferRecordOfOmitsZeroExpirySoExportIsReimportable(t *testing.T) {
	rec := hostAuthRecord{Stored: credential{AccessToken: "access-a", UID: "uid-a", Domain: "copilot.tencent.com"}}
	got, err := transferRecordOf(rec)
	if err != nil {
		t.Fatalf("transferRecordOf returned error: %v", err)
	}
	if got.ExpiresAt != 0 || got.RefreshExpiresAt != 0 {
		t.Fatalf("zero expiry must stay omitted, got expiresAt=%d refreshExpiresAt=%d", got.ExpiresAt, got.RefreshExpiresAt)
	}
	// Round trip through JSON as the panel would.
	preview, err := previewTransferFile(mustJSON(t, map[string]any{"accounts": []transferRecord{got}}))
	if err != nil {
		t.Fatalf("exported record could not be re-imported: %v", err)
	}
	if !preview.Accounts[0].Importable {
		t.Fatalf("exported record is not importable: %#v", preview.Accounts[0])
	}
}

func TestTransferRecordOfUsesReferenceFieldsAndMilliseconds(t *testing.T) {
	expires := time.UnixMilli(1_725_123_456_789).UTC()
	rec := hostAuthRecord{Stored: credential{AccessToken: "access-a", RefreshToken: "refresh-a", UID: "uid-a", DisplayName: "账号A", Domain: "copilot.tencent.com", ExpiresAt: expires}}
	got, err := transferRecordOf(rec)
	if err != nil {
		t.Fatalf("transferRecordOf returned error: %v", err)
	}
	if got.AccessToken != "access-a" || got.UID != "uid-a" || got.ExpiresAt != expires.UnixMilli() {
		t.Fatalf("unexpected export record: %#v", got)
	}
	if !strings.Contains(fmt.Sprintf("%#v", got.AuthRaw), "auth") {
		t.Fatalf("export lost auth_raw: %#v", got.AuthRaw)
	}
}

func TestTransferRecordRoundTripPreservesMillisecondExpiry(t *testing.T) {
	original := time.UnixMilli(1_725_123_456_789).UTC()
	record := transferRecord{AccessToken: "token", UID: "uid", Domain: "copilot.tencent.com", ExpiresAt: original.UnixMilli()}
	credential, err := credentialFromTransferRecord(record, mustSite("workbuddy"))
	if err != nil {
		t.Fatalf("credentialFromTransferRecord returned error: %v", err)
	}
	if credential.ExpiresAt.UnixMilli() != original.UnixMilli() {
		t.Fatalf("expiry changed: got %d want %d", credential.ExpiresAt.UnixMilli(), original.UnixMilli())
	}
}

func TestTransferRegionResolvesSitesAndRejectsUnknownDomains(t *testing.T) {
	cases := map[string]string{
		"copilot.tencent.com":              "workbuddy",
		"https://copilot.tencent.com/path": "workbuddy",
		"www.workbuddy.ai":                 "workbuddy-ai",
		"codebuddy.ai":                     "workbuddy-ai",
		"unknown.example":                  "",
		"":                                 "",
	}
	for domain, want := range cases {
		if got := transferRegion(domain); got != want {
			t.Errorf("transferRegion(%q) = %q, want %q", domain, got, want)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustSite(id string) site {
	got, ok := selectSite(id)
	if !ok {
		panic("unknown test site: " + id)
	}
	return got
}
