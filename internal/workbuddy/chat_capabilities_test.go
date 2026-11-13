package workbuddy_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"cpa-workbuddy/internal/workbuddy"
)

const chatCapabilityConfig = `{"agents":[{"name":"cli","models":["common","glm-5.3","locked","deepseek-v4.1-flash","deepseek-v4.1-flash-sg","claude-opus-5.5"]}],"models":[{"id":"common","maxInputTokens":128000,"maxOutputTokens":8192,"supportsImages":true,"reasoning":{"supportedEfforts":["low","high"],"defaultEffort":"high","canDisableThinking":true}},{"id":"glm-5.3","supportsImages":true},{"id":"locked","onlyReasoning":true,"reasoning":{"supportedEfforts":["high"],"canDisableThinking":false}},{"id":"deepseek-v4.1-flash","onlyReasoning":true,"reasoning":{"supportedEfforts":["low","high"],"defaultEffort":"high","canDisableThinking":false}},{"id":"deepseek-v4.1-flash-sg","onlyReasoning":true,"reasoning":{"supportedEfforts":["low","high"],"defaultEffort":"high","canDisableThinking":false}},{"id":"claude-opus-5.5","supportsImages":true,"reasoning":{"supportedEfforts":["high"]}}]}`

func discoverChatCapabilities(t *testing.T, site, enterprise, config string) (workbuddy.AuthData, workbuddy.ModelResponse) {
	t.Helper()
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, site, enterprise)
	result, err := invoke(s, context.Background(), "model.for_auth", map[string]any{"AuthID": auth.ID, "AuthProvider": auth.Provider, "StorageJSON": auth.StorageJSON}, func(method string, _ []byte) ([]byte, error) {
		switch method {
		case "host.http.do":
			return success(json.RawMessage(config))
		case "host.log":
			return []byte(`{"ok":true,"result":{}}`), nil
		default:
			return nil, errors.New("unexpected discovery callback")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	models := result.(workbuddy.ModelResponse)
	if models.AuthUpdate.ID == "" {
		t.Fatal("discovery did not install capability snapshot")
	}
	return models.AuthUpdate, models
}

func TestChatCapabilityImagesFollowAccountAndSite(t *testing.T) {
	for _, site := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(site, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth, _ := discoverChatCapabilities(t, site, "vision", chatCapabilityConfig)
			prefix := "wb/"
			if site == "workbuddy-ai" {
				prefix = "wb-ai/"
			}
			parts := `[{"type":"text","text":"Describe both"},{"type":"image_url","image_url":{"url":"https://images.invalid/picture.png","detail":"high"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk=","detail":"low"}}]`
			payload := []byte(`{"messages":[{"role":"user","content":` + parts + `}],"max_tokens":64}`)
			body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, prefix+"common", payload)))
			var messages []struct {
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(body["messages"], &messages); err != nil {
				t.Fatal(err)
			}
			if len(messages) != 2 || string(messages[1].Content) != parts {
				t.Fatalf("image content changed = %s", body["messages"])
			}
			if site == "workbuddy" {
				executeRequestProtocol(t, s, requestProtocolInput(auth, prefix+"glm-5.3", payload))
			}
			textOnly, _ := discoverChatCapabilities(t, site, "text", `{"agents":[{"name":"cli","models":["common"]}],"models":[{"id":"common","supportsImages":false}]}`)
			for _, tc := range []struct {
				auth  workbuddy.AuthData
				model string
			}{{textOnly, prefix + "common"}, {parsedIdentity(t, site, "unknown"), prefix + "common"}, {auth, prefix + "not-discovered"}} {
				_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(tc.auth, tc.model, payload), func(string, []byte) ([]byte, error) {
					t.Error("unadvertised image reached HTTP")
					return nil, errors.New("unexpected callback")
				})
				var detail *workbuddy.Error
				if !errors.As(err, &detail) || detail.Code != "unsupported_feature" {
					t.Fatalf("image permission error=%v", err)
				}
			}
			if site == "workbuddy-ai" {
				_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, prefix+"glm-5.3", payload), func(string, []byte) ([]byte, error) {
					t.Error("international GLM image reached HTTP")
					return nil, errors.New("unexpected callback")
				})
				var detail *workbuddy.Error
				if !errors.As(err, &detail) || detail.Code != "unsupported_feature" {
					t.Fatalf("international image error=%v", err)
				}
			}
		})
	}
}

func TestChatCapabilityDefaultAndExplicitEffort(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth, _ := discoverChatCapabilities(t, "workbuddy", "", chatCapabilityConfig)
	for _, tc := range []struct{ extra, want string }{
		{``, `"high"`}, {`,"max_tokens":64`, ``}, {`,"max_tokens":1024`, ``}, {`,"max_tokens":1025`, `"high"`}, {`,"max_tokens":0`, `"high"`},
		{`,"max_completion_tokens":64`, ``}, {`,"max_tokens":64,"max_completion_tokens":32000`, ``}, {`,"max_tokens":null,"max_completion_tokens":64`, ``},
		{`,"reasoning_effort":"none","max_tokens":64`, `"none"`}, {`,"reasoning_effort":"low"`, `"low"`}, {`,"reasoning":{"effort":"low","summary":"auto"}`, ``},
		{`,"reasoning_effort":"low","reasoning":{"effort":"low"}`, `"low"`},
		{`,"reasoning_effort":null`, `null`}, {`,"reasoning_effort":false`, `false`}, {`,"reasoning_effort":""`, `""`},
		{`,"reasoning":null`, `"high"`}, {`,"reasoning":false`, `"high"`}, {`,"reasoning":{}`, `"high"`}, {`,"reasoning":{"effort":null}`, ``},
	} {
		payload := []byte(`{"messages":[{"role":"user","content":"classify"}]` + tc.extra + `}`)
		body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/common", payload)))
		if string(body["reasoning_effort"]) != tc.want {
			t.Fatalf("extra %s effort = %s, want %s", tc.extra, body["reasoning_effort"], tc.want)
		}
		if strings.Contains(tc.extra, `"summary"`) && string(body["reasoning"]) != `{"effort":"low","summary":"auto"}` {
			t.Fatalf("nested explicit effort changed: %s", body["reasoning"])
		}
	}
	for _, model := range []string{"deepseek-v4.1-flash", "deepseek-v4.1-flash-sg"} {
		body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/"+model, []byte(`{"messages":[{"role":"user","content":"classify"}],"reasoning_effort":"none"}`))))
		if string(body["reasoning_effort"]) != `"none"` {
			t.Fatalf("OFF_TAKEN %s effort=%s", model, body["reasoning_effort"])
		}
	}
	for _, tc := range []struct{ model, extra, code string }{
		{"common", `,"reasoning_effort":"max"`, "unsupported_feature"}, {"locked", `,"reasoning_effort":"none"`, "unsupported_feature"},
		{"common", `,"reasoning_effort":true`, "invalid_request"}, {"common", `,"reasoning":true`, "invalid_request"},
		{"common", `,"reasoning_effort":"low","reasoning":{"effort":"high"}`, "invalid_request"},
	} {
		_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb/"+tc.model, []byte(`{"messages":[{"role":"user","content":"classify"}]`+tc.extra+`}`)), func(string, []byte) ([]byte, error) {
			t.Error("invalid reasoning reached HTTP")
			return nil, errors.New("unexpected callback")
		})
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != tc.code {
			t.Fatalf("effort %s/%s error=%v", tc.model, tc.extra, err)
		}
	}
}

func TestChatCapabilityModelPublicationMatchesExecutableRoutes(t *testing.T) {
	for _, site := range []string{"workbuddy", "workbuddy-ai"} {
		_, models := discoverChatCapabilities(t, site, "", chatCapabilityConfig)
		for _, model := range models.Models {
			wantFormat := "openai"
			if site == "workbuddy" && strings.HasSuffix(model.ID, "claude-opus-5.5") {
				wantFormat = "claude"
			}
			if model.NativeFormat != wantFormat {
				t.Fatalf("model %s native format=%s, want %s", model.ID, model.NativeFormat, wantFormat)
			}
			if strings.HasSuffix(model.ID, "claude-opus-5.5") && site == "workbuddy" {
				if model.Thinking == nil || !reflect.DeepEqual(model.Thinking.Levels, []string{"none", "high", "max"}) || !reflect.DeepEqual(model.SupportedInputModalities, []string{"text", "image"}) {
					t.Fatal("native Messages model lost its discovered capabilities")
				}
				continue
			}
			if !reflect.DeepEqual(model.SupportedOutputModalities, []string{"text"}) {
				t.Fatalf("output modalities=%v", model.SupportedOutputModalities)
			}
			if strings.HasSuffix(model.ID, "common") {
				if model.ContextLength != 128000 || model.MaxCompletionTokens != 8192 || !reflect.DeepEqual(model.SupportedInputModalities, []string{"text", "image"}) || model.Thinking == nil || !model.Thinking.ZeroAllowed || !reflect.DeepEqual(model.Thinking.Levels, []string{"none", "low", "high"}) {
					t.Fatalf("common model=%+v", model)
				}
			}
			if site == "workbuddy-ai" && strings.HasSuffix(model.ID, "glm-5.3") && !reflect.DeepEqual(model.SupportedInputModalities, []string{"text"}) {
				t.Fatal("international GLM advertised images")
			}
			if strings.HasSuffix(model.ID, "locked") && (model.Thinking == nil || model.Thinking.ZeroAllowed || !reflect.DeepEqual(model.Thinking.Levels, []string{"high"})) {
				t.Fatal("locked model advertised disable")
			}
		}
	}
}

func TestChatCapabilityRejectsMalformedImagesAndForeignSnapshots(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth, _ := discoverChatCapabilities(t, "workbuddy", "", chatCapabilityConfig)
	for _, image := range []string{`null`, `"url"`, `{}`, `{"url":null}`, `{"url":"x","detail":false}`} {
		payload := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":` + image + `}]}]}`)
		_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb/common", payload), func(string, []byte) ([]byte, error) {
			t.Error("malformed image reached HTTP")
			return nil, errors.New("unexpected callback")
		})
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "invalid_request" {
			t.Fatalf("image %s error=%v", image, err)
		}
	}
	var stored map[string]any
	if err := json.Unmarshal(auth.StorageJSON, &stored); err != nil {
		t.Fatal(err)
	}
	stored["model_snapshot"].(map[string]any)["auth_id"] = "another-account"
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	auth.StorageJSON = encoded
	for _, extra := range []string{`,"reasoning_effort":"high"`, `,"reasoning":{"effort":"low"}`} {
		_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb/common", []byte(`{"messages":[{"role":"user","content":"classify"}]`+extra+`}`)), func(string, []byte) ([]byte, error) {
			t.Error("foreign snapshot reasoning reached HTTP")
			return nil, errors.New("unexpected callback")
		})
		var detail *workbuddy.Error
		if !errors.As(err, &detail) || detail.Code != "unsupported_feature" {
			t.Fatalf("foreign snapshot error=%v", err)
		}
	}
}

func TestChatImageOnlyTurnsHaveStableScopedIdentity(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth, _ := discoverChatCapabilities(t, "workbuddy", "image-one", chatCapabilityConfig)
	other, _ := discoverChatCapabilities(t, "workbuddy", "image-two", chatCapabilityConfig)
	image := `{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}}]}`
	changedImage := `{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,Ynk="}}]}`
	ask := func(account workbuddy.AuthData, messages string, session bool) map[string][]string {
		t.Helper()
		req := requestProtocolInput(account, "wb/common", []byte(`{"messages":[`+messages+`]}`))
		if session {
			req["Metadata"] = map[string]any{"canonical_session_id": "image-session"}
		}
		return executeRequestProtocol(t, s, req).Headers
	}
	one := ask(auth, image, false)
	again := ask(auth, image, false)
	continued := ask(auth, image+`,{"role":"assistant","content":null,"tool_calls":[{"id":"image-call","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"image-call","content":"done"}`, false)
	next := ask(auth, image+`,{"role":"assistant","content":"answer"},`+changedImage, false)
	foreign := ask(other, image, false)
	for _, key := range []string{"X-Conversation-ID", "X-Conversation-Request-ID"} {
		if len(one[key]) != 1 || one[key][0] == "" || one[key][0] != again[key][0] || one[key][0] != continued[key][0] || one[key][0] == foreign[key][0] {
			t.Fatalf("image identity %s is unstable or crosses accounts", key)
		}
	}
	if one["X-Conversation-ID"][0] != next["X-Conversation-ID"][0] || one["X-Conversation-Request-ID"][0] == next["X-Conversation-Request-ID"][0] {
		t.Fatal("next image user turn lost conversation or retained previous turn")
	}
	withSession := ask(auth, image, true)
	changed := ask(auth, changedImage, true)
	if withSession["X-Conversation-ID"][0] != changed["X-Conversation-ID"][0] || withSession["X-Conversation-Request-ID"][0] == changed["X-Conversation-Request-ID"][0] {
		t.Fatal("changed image did not change the turn within the host session")
	}
}

func TestChatEmptyReasoningControlsPreserveUnadvertisedTextRequests(t *testing.T) {
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	for _, extra := range []string{`"reasoning_effort":null`, `"reasoning_effort":false`, `"reasoning_effort":""`, `"reasoning":null`, `"reasoning":false`, `"reasoning":{"effort":null}`} {
		body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/common", []byte(`{"messages":[{"role":"user","content":"hello"}],`+extra+`}`))))
		var expected map[string]json.RawMessage
		if err := json.Unmarshal([]byte(`{`+extra+`}`), &expected); err != nil {
			t.Fatal(err)
		}
		for key, want := range expected {
			if string(body[key]) != string(want) {
				t.Fatalf("empty %s changed: %s", key, body[key])
			}
		}
	}
}
