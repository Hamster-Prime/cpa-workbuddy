package workbuddy

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// transferRecord is the JSON contract shared with dsh-connect-workbuddy.
//
// A record that cannot be imported does not abort the whole file: the parser
// keeps it and records the reason in problem so preview can show it and the
// merge step can skip it while still importing the remaining entries.
type transferRecord struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	UID              string `json:"uid,omitempty"`
	Nickname         string `json:"nickname,omitempty"`
	Email            string `json:"email,omitempty"`
	Domain           string `json:"domain,omitempty"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	RefreshExpiresAt int64  `json:"refreshExpiresAt,omitempty"`
	AuthRaw          any    `json:"auth_raw,omitempty"`

	problem string
}

type transferPreviewEntry struct {
	Index      int    `json:"index"`
	UID        string `json:"uid,omitempty"`
	Nickname   string `json:"nickname,omitempty"`
	Email      string `json:"email,omitempty"`
	Site       string `json:"site,omitempty"`
	HasToken   bool   `json:"has_token"`
	Importable bool   `json:"importable"`
	Problem    string `json:"problem,omitempty"`
}

type transferPreview struct {
	Accounts []transferPreviewEntry `json:"accounts"`
	Total    int                    `json:"total"`
}

// transferMerge is the outcome of merging selected records into credentials.
// Imported always equals len(Changes): it counts credentials actually written,
// not input records, so duplicate identities cannot inflate it.
type transferMerge struct {
	Changes    map[string]credential
	Imported   int
	Skipped    int
	Duplicated int
}

func parseTransferFile(raw []byte) ([]transferRecord, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, failure("invalid_transfer", "transfer file is empty", 400)
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, failure("invalid_transfer", "transfer file is not valid JSON", 400)
	}
	items, ok := transferItems(parsed)
	if !ok {
		return nil, failure("invalid_transfer", "transfer file must be a JSON array or an accounts/data/records wrapper", 400)
	}
	records := make([]transferRecord, 0, len(items))
	for i, item := range items {
		position := i + 1
		object, ok := item.(map[string]any)
		if !ok {
			records = append(records, transferRecord{problem: fmt.Sprintf("item %d is not an account object", position)})
			continue
		}
		encoded, err := json.Marshal(object)
		if err != nil {
			records = append(records, transferRecord{problem: fmt.Sprintf("item %d cannot be encoded", position)})
			continue
		}
		var record transferRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			records = append(records, transferRecord{problem: fmt.Sprintf("item %d has an invalid field type", position)})
			continue
		}
		record.problem = transferRecordProblem(record, position)
		records = append(records, record)
	}
	return records, nil
}

// transferDomain resolves the domain a record belongs to, falling back to the
// nested auth_raw.auth.domain used by the reference exporter when the flat
// field is absent.
func transferDomain(record transferRecord) string {
	if domain := strings.TrimSpace(record.Domain); domain != "" {
		return domain
	}
	root := authRawRoot(record)
	if root == nil {
		return ""
	}
	if auth, ok := root["auth"].(map[string]any); ok {
		domain, _ := auth["domain"].(string)
		return strings.TrimSpace(domain)
	}
	// A sibling tool may copy a desktop auth document verbatim, which keeps the
	// fields at the root of auth_raw instead of nesting them under auth.
	domain, _ := root["domain"].(string)
	return strings.TrimSpace(domain)
}

// authRawRoot returns the auth_raw document as an object, or nil when the
// record carries none. Both the nested {auth, account} shape this plugin
// exports and a flat desktop auth document are honoured.
func authRawRoot(record transferRecord) map[string]any {
	root, _ := record.AuthRaw.(map[string]any)
	return root
}

// transferRecordProblem reports why a parsed record cannot be imported, or ""
// when it is usable. Missing tokens stay a per-record problem rather than a
// file-level error so one bad entry never blocks a batch.
func transferRecordProblem(record transferRecord, position int) string {
	if strings.TrimSpace(record.AccessToken) == "" {
		return fmt.Sprintf("item %d is missing access_token", position)
	}
	if !safeValue(record.AccessToken) || !safeValue(record.RefreshToken) || !safeValue(record.Domain) {
		return fmt.Sprintf("item %d contains unsafe credential text", position)
	}
	if record.ExpiresAt < 0 || record.RefreshExpiresAt < 0 {
		return fmt.Sprintf("item %d has a negative expiry", position)
	}
	if strings.TrimSpace(record.UID) == "" {
		uid, _ := authRawAccount(record)["uid"].(string)
		if strings.TrimSpace(uid) == "" {
			return fmt.Sprintf("item %d is missing uid", position)
		}
	}
	if _, ok := selectSite(transferRegion(transferDomain(record))); !ok {
		return fmt.Sprintf("item %d has an unsupported domain", position)
	}
	return ""
}

func authRawAccount(record transferRecord) map[string]any {
	root := authRawRoot(record)
	if root == nil {
		return nil
	}
	if account, ok := root["account"].(map[string]any); ok {
		return account
	}
	// Flat desktop auth documents put uid/nickname at the root.
	if _, ok := root["uid"]; ok {
		return root
	}
	if _, ok := root["nickname"]; ok {
		return root
	}
	return nil
}

func transferItems(parsed any) ([]any, bool) {
	if items, ok := parsed.([]any); ok {
		return items, true
	}
	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, false
	}
	for _, key := range []string{"accounts", "data", "records"} {
		if items, ok := object[key].([]any); ok {
			return items, true
		}
	}
	for _, key := range []string{"data", "result"} {
		if nested, ok := object[key].(map[string]any); ok {
			for _, inner := range []string{"accounts", "data", "records"} {
				if items, ok := nested[inner].([]any); ok {
					return items, true
				}
			}
		}
	}
	return nil, false
}

func previewTransferFile(raw []byte) (transferPreview, error) {
	records, err := parseTransferFile(raw)
	if err != nil {
		return transferPreview{}, err
	}
	preview := transferPreview{Accounts: make([]transferPreviewEntry, len(records)), Total: len(records)}
	for i, record := range records {
		problem := record.problem
		preview.Accounts[i] = transferPreviewEntry{
			Index: i, UID: record.UID, Nickname: record.Nickname, Email: record.Email,
			Site: transferRegion(transferDomain(record)), HasToken: strings.TrimSpace(record.AccessToken) != "",
			Importable: problem == "", Problem: problem,
		}
	}
	return preview, nil
}

func transferRecordOf(rec hostAuthRecord) (transferRecord, error) {
	stored := rec.Stored
	if stored.AccessToken == "" {
		return transferRecord{}, failure("invalid_credentials", "cannot export a credential without an access token", 400)
	}
	record := transferRecord{
		AccessToken:  stored.AccessToken,
		RefreshToken: stored.RefreshToken,
		UID:          stored.UID,
		Nickname:     stored.DisplayName,
		Domain:       stored.Domain,
	}
	// Older credentials can carry no domain even though their site is known,
	// and an upstream may report a domain the importer does not recognise. In
	// both cases export the site endpoint: a file the importer would reject
	// defeats the purpose of exporting it.
	if transferRegion(record.Domain) == "" {
		record.Domain = rec.Site.endpoint
	}
	// A zero expiry must stay omitted: UnixMilli() on the zero time is a large
	// negative number, and the importer rejects negative expiries, which would
	// make exported files impossible to re-import.
	if !stored.ExpiresAt.IsZero() {
		record.ExpiresAt = stored.ExpiresAt.UnixMilli()
	}
	if stored.RefreshExpiresAt != nil && !stored.RefreshExpiresAt.IsZero() {
		record.RefreshExpiresAt = stored.RefreshExpiresAt.UnixMilli()
	}
	auth := map[string]any{
		"accessToken": stored.AccessToken, "refreshToken": stored.RefreshToken,
		"domain": record.Domain,
	}
	// Same rule as the flat expiry: the zero time's Unix seconds are a large
	// negative number, so an unknown expiry stays absent.
	if !stored.ExpiresAt.IsZero() {
		auth["expiresAt"] = stored.ExpiresAt.Unix()
	}
	record.AuthRaw = map[string]any{
		"auth": auth,
		"account": map[string]any{
			"uid": stored.UID, "nickname": stored.DisplayName, "enterpriseId": stored.EnterpriseID,
		},
	}
	return record, nil
}

func credentialFromTransferRecord(record transferRecord, target site) (credential, error) {
	if strings.TrimSpace(record.AccessToken) == "" {
		return credential{}, failure("invalid_transfer", "account is missing access_token", 400)
	}
	if !safeValue(record.AccessToken) || !safeValue(record.RefreshToken) || !safeValue(record.Domain) {
		return credential{}, failure("invalid_transfer", "account contains unsafe credential text", 400)
	}
	stored := credential{
		Provider: provider, Type: provider, CredentialType: "browser", Version: credentialVersion,
		Site: target.id, UID: strings.TrimSpace(record.UID), DisplayName: strings.TrimSpace(record.Nickname),
		AccessToken: record.AccessToken, RefreshToken: record.RefreshToken, Domain: transferDomain(record),
		TokenType: "Bearer", Generation: rand.Text(),
	}
	if stored.UID == "" {
		uid, _ := authRawAccount(record)["uid"].(string)
		stored.UID = strings.TrimSpace(uid)
	}
	if stored.DisplayName == "" {
		nickname, _ := authRawAccount(record)["nickname"].(string)
		stored.DisplayName = strings.TrimSpace(nickname)
	}
	if record.ExpiresAt > 0 {
		stored.ExpiresAt = time.UnixMilli(record.ExpiresAt).UTC()
	} else {
		// The sibling format permits an omitted expiry. CPA requires a non-zero
		// expiry for browser credentials, so let the normal refresh path obtain a
		// current token shortly after import rather than rejecting the record.
		stored.ExpiresAt = time.Now().Add(5 * time.Minute).UTC()
	}
	if record.RefreshExpiresAt > 0 {
		expires := time.UnixMilli(record.RefreshExpiresAt).UTC()
		stored.RefreshExpiresAt = &expires
	}
	if enterprise, ok := authRawAccount(record)["enterpriseId"].(string); ok {
		stored.EnterpriseID = strings.TrimSpace(enterprise)
	}
	if stored.UID == "" {
		return credential{}, failure("invalid_transfer", "account is missing uid", 400)
	}
	return stored, nil
}

// mergeTransferRecords turns the selected records into credentials keyed by
// stable identity. Records that cannot be imported are skipped individually, so
// one malformed entry never rejects the rest of the batch.
func mergeTransferRecords(records []transferRecord, indexes []int) transferMerge {
	merged := transferMerge{Changes: make(map[string]credential, len(indexes))}
	for _, index := range indexes {
		if index < 0 || index >= len(records) {
			merged.Skipped++
			continue
		}
		record := records[index]
		if record.problem != "" {
			merged.Skipped++
			continue
		}
		target, ok := selectSite(transferRegion(transferDomain(record)))
		if !ok {
			merged.Skipped++
			continue
		}
		credential, err := credentialFromTransferRecord(record, target)
		if err != nil {
			merged.Skipped++
			continue
		}
		id, _, err := credentialIdentity(credential)
		if err != nil {
			merged.Skipped++
			continue
		}
		if _, exists := merged.Changes[id]; exists {
			merged.Duplicated++
		}
		merged.Changes[id] = credential
	}
	merged.Imported = len(merged.Changes)
	return merged
}

// importTransferResult is returned by the transfer import endpoint.
type importTransferResult struct {
	Imported   int `json:"imported"`
	Skipped    int `json:"skipped"`
	Duplicated int `json:"duplicated"`
}

// importTransferRecords owns the whole import orchestration: parse, select,
// match existing host credentials by identity, then persist. Keeping it out of
// HandleManagement leaves that dispatcher as routing only.
//
// A host read failure is an error, never a silent skip: treating an unreadable
// existing credential as absent would create a duplicate file and drop the
// host-managed metadata the existing record carries.
func importTransferRecords(call func(string, []byte) ([]byte, error), text string, indexes []int) (importTransferResult, error) {
	records, err := parseTransferFile([]byte(text))
	if err != nil {
		return importTransferResult{}, err
	}
	if len(indexes) == 0 {
		return importTransferResult{}, failure("invalid_transfer", "indexes must contain at least one record", 400)
	}
	merged := mergeTransferRecords(records, indexes)
	result := importTransferResult{Imported: merged.Imported, Skipped: merged.Skipped, Duplicated: merged.Duplicated}
	if len(merged.Changes) == 0 {
		return result, nil
	}

	entries, err := hostAuthList(call)
	if err != nil {
		return importTransferResult{}, err
	}
	existing := make(map[string]hostAuthRecord, len(entries))
	for _, entry := range entries {
		rec, getErr := hostAuthGet(call, entry.AuthIndex)
		if getErr != nil {
			return importTransferResult{}, getErr
		}
		id, _, idErr := credentialIdentity(rec.Stored)
		if idErr != nil {
			continue
		}
		existing[id] = rec
	}

	for id, stored := range merged.Changes {
		auth, authErr := authFromCredential(stored, nil)
		if authErr != nil {
			return importTransferResult{}, failure("invalid_transfer", "imported credential could not be normalized", 400)
		}
		if rec, ok := existing[id]; ok {
			auth.FileName = rec.FileName
			if saveErr := hostAuthSaveCredential(call, rec, auth); saveErr != nil {
				return importTransferResult{}, failure("host_save_failed", "imported credential could not be stored", 502)
			}
			continue
		}
		if saveErr := hostAuthSaveNewCredential(call, auth); saveErr != nil {
			return importTransferResult{}, failure("host_save_failed", "imported credential could not be stored", 502)
		}
	}
	return result, nil
}

func transferRegion(domain string) string {
	host := strings.ToLower(strings.TrimSpace(domain))
	if parsed, err := url.Parse(host); err == nil && parsed.Hostname() != "" {
		host = strings.ToLower(parsed.Hostname())
	}
	host = strings.TrimSuffix(host, ".")
	switch {
	// The CN product signs in under BOTH brand spellings, and real credentials
	// carry either one in `domain`; refusing `codebuddy.cn` rejected this
	// plugin's own export, because www.codebuddy.cn is one of its CN login
	// pages (see trustedLoginURL).
	case host == "copilot.tencent.com" || strings.HasSuffix(host, ".tencent.com") ||
		host == "workbuddy.cn" || strings.HasSuffix(host, ".workbuddy.cn") ||
		host == "codebuddy.cn" || strings.HasSuffix(host, ".codebuddy.cn"):
		return "workbuddy"
	case host == "www.workbuddy.ai" || host == "workbuddy.ai" || host == "codebuddy.ai" ||
		strings.HasSuffix(host, ".workbuddy.ai") || strings.HasSuffix(host, ".codebuddy.ai"):
		return "workbuddy-ai"
	default:
		return ""
	}
}
