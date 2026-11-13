package workbuddy_test

import (
	"context"
	"encoding/json"
	"testing"

	"cpa-workbuddy/internal/workbuddy"
)

func parsedIdentity(t *testing.T, site, enterprise string) workbuddy.AuthData {
	t.Helper()
	s := workbuddy.New()
	defer s.Close()
	raw, err := json.Marshal(map[string]any{
		"provider": "workbuddy", "type": "workbuddy", "credential_type": "browser", "version": 1,
		"site": site, "uid": "person", "enterprise_id": enterprise,
		"display_name": "Person", "access_token": "fixture-browser-access", "expires_at": "2030-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := invoke(s, context.Background(), "auth.parse", map[string]any{"Provider": "workbuddy", "RawJSON": raw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed := result.(workbuddy.ParseResponse)
	if !parsed.Handled {
		t.Fatal("browser credential was not recognized")
	}
	return parsed.Auth
}

func TestRequestHeadersUsePersistedScopeAndFixedSite(t *testing.T) {
	for _, site := range []string{"workbuddy", "workbuddy-ai"} {
		for _, enterprise := range []string{"", "team-one", "team-two"} {
			t.Run(site+"/"+enterprise, func(t *testing.T) {
				auth := parsedIdentity(t, site, enterprise)
				auth.Metadata["enterprise_id"] = "client-enterprise"
				auth.Metadata["domain"] = "client.invalid"
				auth.Attributes["scope"] = "enterprise:client-enterprise"
				headers, err := auth.RequestHeaders()
				if err != nil {
					t.Fatal(err)
				}
				domain := "copilot.tencent.com"
				if site == "workbuddy-ai" {
					domain = "www.workbuddy.ai"
				}
				if len(headers["Authorization"]) != 1 || headers["Authorization"][0] != "Bearer fixture-browser-access" ||
					len(headers["X-User-Id"]) != 1 || headers["X-User-Id"][0] != "person" ||
					len(headers["X-Domain"]) != 1 || headers["X-Domain"][0] != domain {
					t.Fatal("request identity headers did not match the persisted browser credential")
				}
				for _, key := range []string{"X-Enterprise-Id", "X-Tenant-Id"} {
					values := headers[key]
					if enterprise == "" {
						if len(values) != 0 {
							t.Fatal("personal request acquired an enterprise scope")
						}
					} else if len(values) != 1 || values[0] != enterprise {
						t.Fatal("request enterprise scope was overridden by mutable metadata")
					}
				}
				headers["Authorization"][0] = "client-override"
				again, err := auth.RequestHeaders()
				if err != nil || again["Authorization"][0] != "Bearer fixture-browser-access" {
					t.Fatal("caller header mutation changed a later request's credentials")
				}
			})
		}
	}
}

func TestRequestHeadersRejectStorageFromAnotherIdentity(t *testing.T) {
	for _, field := range []string{"site", "uid", "enterprise_id"} {
		t.Run(field, func(t *testing.T) {
			auth := parsedIdentity(t, "workbuddy", "team-one")
			var raw map[string]any
			if err := json.Unmarshal(auth.StorageJSON, &raw); err != nil {
				t.Fatal(err)
			}
			value := "another-identity"
			if field == "site" {
				value = "workbuddy-ai"
			}
			raw[field] = value
			storage, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			auth.StorageJSON = storage
			if _, err := auth.RequestHeaders(); err == nil {
				t.Fatal("selected auth ID accepted another identity's stored credential")
			}
		})
	}
}

func TestRequestHeadersBindOptionalFileName(t *testing.T) {
	auth := parsedIdentity(t, "workbuddy", "team-one")
	auth.FileName = ""
	headers, err := auth.RequestHeaders()
	if err != nil || len(headers["X-Enterprise-Id"]) != 1 || headers["X-Enterprise-Id"][0] != "team-one" {
		t.Fatal("auth-ID-only host request lost its enterprise binding")
	}
	auth.FileName = "another-identity.json"
	if headers, err := auth.RequestHeaders(); err == nil || headers != nil {
		t.Fatal("credential accepted a filename belonging to another identity")
	}
}

func TestBrowserPersistenceExplicitlyClearsOwnedOptionalFields(t *testing.T) {
	auth := parsedIdentity(t, "workbuddy", "")
	var stored map[string]any
	if err := json.Unmarshal(auth.StorageJSON, &stored); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"enterprise_id", "refresh_token", "domain", "token_type", "refresh_expires_at"} {
		value, present := stored[key]
		if !present || (key == "refresh_expires_at" && value != nil) || (key != "refresh_expires_at" && value != "") {
			t.Errorf("credential does not explicitly clear owned field %s", key)
		}
		if _, present := auth.Metadata[key]; !present {
			t.Errorf("host merge could restore a stale owned field %s", key)
		}
	}
}
