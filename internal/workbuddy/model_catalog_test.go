package workbuddy_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"cpa-workbuddy/internal/workbuddy"
)

func parseCatalog(t *testing.T, site, config string) []workbuddy.CatalogModel {
	t.Helper()
	models, err := workbuddy.ParseModelConfig(site, []byte(config), time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return models
}

func TestModelCatalogPreservesAccountOrderIDsAndDistinctNames(t *testing.T) {
	const config = `{
		"agents":[{"name":"compact","models":["desktop"]},{"name":"cli","models":["custom:gpt-5.6-sol|gpt-5.6-sol","deepseek-v4.1-flash","deepseek-v4.1-flash"]},{"name":"cli","models":["deepseek-v4.1-flash-sg","other","unknown","deepseek-v4.1-flash"]}],
		"models":[
			{"id":"deepseek-v4.1-flash","name":"Deepseek-V4.1-Flash","maxInputTokens":1000000,"maxOutputTokens":128000},
			{"id":"deepseek-v4.1-flash-sg","name":"Deepseek-V4.1-Flash"},
			{"id":"other","name":"Deepseek-V4.1-Flash"},
			{"id":"custom:claude-opus-5-5|claude-opus-5-5","name":"claude-opus-5-5","modelType":"enterprise","maxInputTokens":256000,"maxOutputTokens":64000,"supportsImages":true,"supportsReasoning":true,"reasoning":{"supportedEfforts":["minimal","low","medium","high","xhigh","max"],"defaultEffort":"medium","canDisableThinking":true}},
			{"id":"custom:gpt-5.6-sol|gpt-5.6-sol","name":"gpt-5.6-sol"},
			{"id":"custom:claude-opus-5-5|claude-opus-5-5","name":"duplicate detail"},
			{"id":"desktop","name":"Local","modelType":"custom","url":"https://local.invalid","apiKey":"unused"},
			{"id":"desktop-chat","modelType":"chat"},{"id":"unlisted"}
		]
	}`
	for _, site := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(site, func(t *testing.T) {
			models := parseCatalog(t, site, config)
			var ids, names []string
			for _, model := range models {
				ids = append(ids, model.ID)
				names = append(names, model.Name)
			}
			if !reflect.DeepEqual(ids, []string{"custom:gpt-5.6-sol|gpt-5.6-sol", "deepseek-v4.1-flash", "deepseek-v4.1-flash-sg", "other", "unknown", "custom:claude-opus-5-5|claude-opus-5-5"}) {
				t.Fatalf("ordered IDs = %v", ids)
			}
			if !reflect.DeepEqual(names, []string{"gpt-5.6-sol", "Deepseek-V4.1-Flash", "Deepseek-V4.1-Flash (SG)", "Deepseek-V4.1-Flash (other)", "unknown", "claude-opus-5-5"}) {
				t.Fatalf("names = %v", names)
			}
			if models[1].ContextLength != 1000000 || models[1].MaxOutputTokens != 128000 {
				t.Fatal("source limits were capped or replaced")
			}
			if models[4].ContextLength != 0 || models[4].MaxOutputTokens != 0 || models[4].Images || models[4].Free != nil || models[4].CreditRate != nil {
				t.Fatal("missing details invented metadata")
			}
			custom := models[5]
			if custom.ContextLength != 256000 || custom.MaxOutputTokens != 64000 || !custom.Images || custom.DefaultEffort != "medium" || custom.Free != nil || custom.CreditRate != nil ||
				!reflect.DeepEqual(custom.Efforts, []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}) {
				t.Fatalf("enterprise metadata = %#v", custom)
			}
		})
	}
}

func TestModelCatalogEmptyAndTypedFailures(t *testing.T) {
	for _, config := range []string{`{"agents":[{"name":"cli","models":[]}]}`, `{"agents":[{"name":"cli"}],"models":[]}`} {
		models := parseCatalog(t, "workbuddy", config)
		if models == nil || len(models) != 0 {
			t.Fatalf("valid empty CLI = %#v", models)
		}
	}
	cases := []struct {
		name, site, config, code string
		status                   int
	}{
		{"unknown site", "https://copilot.tencent.com", `{}`, "invalid_site", 400},
		{"missing CLI", "workbuddy", `{"agents":[{"name":"compact","models":[]}],"models":[{"id":"custom:a|a"}]}`, "missing_cli_config", 502},
		{"absent agents", "workbuddy-ai", `{"models":[]}`, "missing_cli_config", 502},
		{"invalid JSON", "workbuddy", `{`, "invalid_model_config", 502},
		{"trailing JSON", "workbuddy", `{"agents":[]} {}`, "invalid_model_config", 502},
		{"null data", "workbuddy", `null`, "invalid_model_config", 502},
		{"array data", "workbuddy", `[]`, "invalid_model_config", 502},
		{"agents object", "workbuddy", `{"agents":{}}`, "invalid_model_config", 502},
		{"null agent", "workbuddy", `{"agents":[null]}`, "invalid_model_config", 502},
		{"missing agent name", "workbuddy", `{"agents":[{"models":[]}]}`, "invalid_model_config", 502},
		{"models object", "workbuddy", `{"agents":[{"name":"cli"}],"models":{}}`, "invalid_model_config", 502},
		{"CLI model number", "workbuddy", `{"agents":[{"name":"cli","models":[42]}]}`, "invalid_model_config", 502},
		{"CLI model empty", "workbuddy", `{"agents":[{"name":"cli","models":[""]}]}`, "invalid_model_config", 502},
		{"CLI model whitespace", "workbuddy", `{"agents":[{"name":"cli","models":[" a"]}]}`, "invalid_model_config", 502},
		{"CLI model control", "workbuddy", `{"agents":[{"name":"cli","models":["a\u0000b"]}]}`, "invalid_model_config", 502},
		{"null detail", "workbuddy", `{"agents":[{"name":"cli"}],"models":[null]}`, "invalid_model_config", 502},
		{"detail missing ID", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{}]}`, "invalid_model_config", 502},
		{"detail numeric ID", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":12}]}`, "invalid_model_config", 502},
		{"negative context", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","maxInputTokens":-1}]}`, "invalid_model_config", 502},
		{"fractional output", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","maxOutputTokens":1.5}]}`, "invalid_model_config", 502},
		{"overflow context", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","maxInputTokens":9223372036854775808}]}`, "invalid_model_config", 502},
		{"string context", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","maxInputTokens":"1000"}]}`, "invalid_model_config", 502},
		{"numeric overflow credits", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","credits":1e999}]}`, "invalid_model_config", 502},
		{"structured credits", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","credits":{}}]}`, "invalid_model_config", 502},
		{"invalid images", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","supportsImages":"true"}]}`, "invalid_model_config", 502},
		{"invalid reasoning", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","reasoning":[]}]}`, "invalid_model_config", 502},
		{"invalid efforts", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","reasoning":{"supportedEfforts":[3]}}]}`, "invalid_model_config", 502},
		{"empty effort", "workbuddy", `{"agents":[{"name":"cli"}],"models":[{"id":"a","reasoning":{"supportedEfforts":[""]}}]}`, "invalid_model_config", 502},
		{"invalid promotions", "workbuddy", `{"agents":[{"name":"cli"}],"modelPromotions":{}}`, "invalid_model_config", 502},
		{"invalid daily schedule", "workbuddy", `{"agents":[{"name":"cli"}],"modelPromotions":[{"schedule":{"daily":{}}}]}`, "invalid_model_config", 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models, err := workbuddy.ParseModelConfig(tc.site, []byte(tc.config), time.Time{})
			var typed *workbuddy.Error
			if models != nil || !errors.As(err, &typed) || typed.Code != tc.code || typed.HTTPStatus != tc.status {
				t.Fatalf("result = %#v, error = %#v", models, err)
			}
		})
	}
}

func TestModelCatalogReasoningAndSiteImageRules(t *testing.T) {
	const config = `{"agents":[{"name":"cli","models":["glm-5.3","GLM-5.3","kimi-k3","disabled","claude-opus-5.5","CLAUDE-sonnet-5.5","custom:claude-x|claude-x","deepseek-v4.1-flash","deepseek-v4.1-flash-sg","fallback","fallback-empty","default-invalid","no-reasoning"]}],"models":[
		{"id":"glm-5.3","supportsImages":true},
		{"id":"GLM-5.3","supportsImages":true},
		{"id":"kimi-k3","supportsImages":true,"supportsReasoning":true,"onlyReasoning":true,"reasoning":{"effort":"medium"}},
		{"id":"disabled","supportsImages":false,"supportsReasoning":true,"reasoning":{"supportedEfforts":["high"],"canDisableThinking":false}},
		{"id":"claude-opus-5.5","reasoning":{"supportedEfforts":["low","xhigh"],"defaultEffort":"max"}},
		{"id":"CLAUDE-sonnet-5.5","reasoning":{"supportedEfforts":["max"],"defaultEffort":"max"}},
		{"id":"custom:claude-x|claude-x","reasoning":{"supportedEfforts":["high"]}},
		{"id":"deepseek-v4.1-flash","onlyReasoning":true,"reasoning":{"supportedEfforts":["low","high","max"],"canDisableThinking":false}},
		{"id":"deepseek-v4.1-flash-sg","supportsReasoning":true,"onlyReasoning":true,"reasoning":{"effort":"high","canDisableThinking":false}},
		{"id":"fallback","supportsReasoning":true,"reasoning":{"effort":"xhigh"}},
		{"id":"fallback-empty","supportsReasoning":true,"reasoning":{"supportedEfforts":[],"effort":"invalid"}},
		{"id":"default-invalid","reasoning":{"supportedEfforts":["high"],"defaultEffort":"invalid","effort":"high"}},
		{"id":"no-reasoning","reasoning":{"effort":"high"}}
	]}`
	for _, site := range []string{"workbuddy", "workbuddy-ai"} {
		t.Run(site, func(t *testing.T) {
			models := parseCatalog(t, site, config)
			if models[0].Images != (site == "workbuddy") || !models[1].Images || !models[2].Images || models[3].Images {
				t.Fatal("supportsImages or case-sensitive international GLM suppression changed")
			}
			wants := [][]string{nil, nil, {"low", "medium", "high", "max"}, {"high"}, {"none", "low", "xhigh", "max"}, {"none", "max"}, {"none", "high"}, {"none", "low", "high", "max"}, {"none", "low", "high", "max"}, {"none", "low", "high", "xhigh", "max"}, {"none", "low", "high", "max"}, {"none", "high"}, nil}
			if site == "workbuddy-ai" {
				wants[4] = []string{"none", "low", "xhigh"}
			}
			for i, want := range wants {
				if !reflect.DeepEqual(models[i].Efforts, want) {
					t.Errorf("%s efforts = %v, want %v", models[i].ID, models[i].Efforts, want)
				}
			}
			if models[2].DefaultEffort != "medium" || models[8].DefaultEffort != "high" || models[9].DefaultEffort != "xhigh" || models[10].DefaultEffort != "" || models[11].DefaultEffort != "" {
				t.Fatal("default effort did not follow supported levels and explicit default precedence")
			}
			wantDefault := ""
			if site == "workbuddy" {
				wantDefault = "max"
			}
			if models[4].DefaultEffort != wantDefault {
				t.Fatal("native CN max did not match the source route classifier")
			}
		})
	}
}

func TestModelCatalogConfigurationFixtures(t *testing.T) {
	fixture, err := os.ReadFile("testdata/config-561.json")
	if err != nil {
		t.Fatal(err)
	}
	var sites map[string]struct{ Data json.RawMessage }
	if err := json.Unmarshal(fixture, &sites); err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"workbuddy", "workbuddy-ai"} {
		models, err := workbuddy.ParseModelConfig(site, sites[site].Data, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs := []string{"deepseek-v4.1-flash", "glm-5.3-flash", "deepseek-v4-pro"}
		wantOutputs := []int64{128000, 131072, 128000}
		if site == "workbuddy-ai" {
			wantIDs = []string{"deepseek-v4.1-flash", "deepseek-v4.1-flash-sg", "glm-5.3-flash"}
			wantOutputs = []int64{128000, 128000, 32000}
		}
		if len(models) != 3 {
			t.Fatalf("%s model count = %d", site, len(models))
		}
		for i, model := range models {
			if model.ID != wantIDs[i] || model.ContextLength != 1000000 || model.MaxOutputTokens != wantOutputs[i] {
				t.Errorf("%s source limits = %#v", site, model)
			}
		}
	}
	fixture, err = os.ReadFile("testdata/config-efforts.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct{ Data json.RawMessage }
	if err := json.Unmarshal(fixture, &envelope); err != nil {
		t.Fatal(err)
	}
	models, err := workbuddy.ParseModelConfig("workbuddy-ai", envelope.Data, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"fast-model":             {"low", "medium", "high", "max"},
		"primary-model":          {"low", "high", "max"},
		"hy3":                    {"low", "high"},
		"deepseek-v4.1-flash-sg": {"none", "low", "high", "max"},
		"gpt-5.6-sol":            {"none", "low", "medium", "high", "xhigh", "max"},
		"gemini-3.5-flash":       {"low", "medium", "high", "max"},
		"glm-5.2":                {"none", "high", "xhigh"},
		"kimi-k3":                {"low", "medium", "high", "max"},
	}
	if len(models) != len(want) {
		t.Fatalf("fixture count = %d", len(models))
	}
	for _, model := range models {
		efforts, found := want[model.ID]
		if !found || !reflect.DeepEqual(model.Efforts, efforts) {
			t.Errorf("%s efforts = %v", model.ID, model.Efforts)
		}
	}
	if models[0].CreditRate == nil || *models[0].CreditRate != 0.34 || models[7].MaxOutputTokens != 32000 || !models[7].Images || models[6].Images {
		t.Fatal("fixture credit units, output limits or image declarations were lost")
	}
}

func TestModelCatalogSiteExclusions(t *testing.T) {
	const cfg = `{"agents":[{"name":"cli","models":["auto","default-model","max-model","gpt-5"]}],"models":[]}`
	cnModels := parseCatalog(t, "workbuddy", cfg)
	cnIDs := make([]string, len(cnModels))
	for i, m := range cnModels {
		cnIDs[i] = m.ID
	}
	if !reflect.DeepEqual(cnIDs, []string{"default-model", "max-model", "gpt-5"}) {
		t.Fatalf("workbuddy models = %v, want auto excluded", cnIDs)
	}

	intlModels := parseCatalog(t, "workbuddy-ai", cfg)
	intlIDs := make([]string, len(intlModels))
	for i, m := range intlModels {
		intlIDs[i] = m.ID
	}
	if !reflect.DeepEqual(intlIDs, []string{"auto", "gpt-5"}) {
		t.Fatalf("workbuddy-ai models = %v, want default-model and max-model excluded", intlIDs)
	}
}

func TestModelCatalogCreditUnitsAndUnknownPrices(t *testing.T) {
	cases := []struct {
		credits string
		rate    *float64
	}{
		{`"x0.34 credits"`, new(0.34)}, {`"x0.59 credit"`, new(0.59)},
		{`" X0.99 CREDITS "`, new(0.99)}, {`"x0.00"`, new(float64(0))},
		{`0`, new(float64(0))}, {`0.29`, new(0.29)},
		{`""`, nil}, {`null`, nil}, {`"credits"`, nil}, {`"x0.34 bananas"`, nil},
		{`"xNaN credits"`, nil}, {`"xInfinity credits"`, nil}, {`"x-1 credits"`, nil}, {`-1`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.credits, func(t *testing.T) {
			model := parseCatalog(t, "workbuddy-ai", `{"agents":[{"name":"cli","models":["a"]}],"models":[{"id":"a","credits":`+tc.credits+`}]}`)[0]
			if !reflect.DeepEqual(model.CreditRate, tc.rate) || model.CreditRateWas != nil {
				t.Fatalf("credit rate = %v, previous = %v", model.CreditRate, model.CreditRateWas)
			}
			if tc.rate == nil {
				if model.Free != nil {
					t.Fatal("unknown credits were treated as a known free or paid price")
				}
			} else if model.Free == nil || *model.Free != (*tc.rate == 0) {
				t.Fatal("free metadata did not match the known credit price")
			}
		})
	}
}

func TestModelCatalogPromotionPricesAndDailyRestrictions(t *testing.T) {
	const config = `{"agents":[{"name":"cli","models":["night","night-zero","night-unknown","badge-only","whole","factor","unknown","disabled","future","expired","boundary","best"]}],"models":[
		{"id":"night","credits":"x0.29"},{"id":"night-zero","credits":"x0.00"},{"id":"night-unknown"},{"id":"badge-only","credits":"x0.11"},
		{"id":"whole","credits":"x0.40"},{"id":"factor","credits":2},{"id":"unknown"},{"id":"disabled","credits":1},
		{"id":"future","credits":1},{"id":"expired","credits":1},{"id":"boundary","credits":1},{"id":"best","credits":2}
	],"modelPromotions":[
		{"kind":"discount","modelIds":["night","night-zero","night-unknown"],"discount":{"discountedCredits":"0.00x","factor":0},"schedule":{"daily":[{"start":"23:00","end":"8:00"}]}},
		{"kind":"discount","modelIds":["badge-only"],"schedule":{"daily":[{"start":"0:00","end":"23:59"}]}},
		{"kind":"discount","modelIds":["whole"],"discount":{"discountedCredits":"0x","factor":0.5},"schedule":{"validFrom":"2026-10-12T00:00:00+08:00","validUntil":"2026-10-26T00:00:00+08:00"}},
		{"kind":"discount","modelIds":["factor"],"discount":{"factor":0.25}},
		{"kind":"discount","modelIds":["unknown"],"discount":{"discountedCredits":"0.50x"}},
		{"kind":"discount","modelIds":["disabled"],"enabled":false,"discount":{"factor":0}},
		{"kind":"discount","modelIds":["future"],"discount":{"factor":0},"schedule":{"validFrom":"2027-01-01T00:00:00Z"}},
		{"kind":"discount","modelIds":["expired"],"discount":{"factor":0},"schedule":{"validUntil":"2026-10-14T09:00:00Z"}},
		{"kind":"discount","modelIds":["boundary"],"discount":{"factor":0},"schedule":{"validFrom":"2026-10-14T09:00:00Z"}},
		{"kind":"discount","modelIds":["best"],"discount":{"factor":0.5}},{"kind":"discount","modelIds":["best"],"discount":{"discountedCredits":"0.25x"}},
		{"kind":"badge","modelIds":["best"],"discount":{"factor":0}}
	]}`
	for _, at := range []time.Time{time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 14, 15, 30, 0, 0, time.UTC)} {
		models, err := workbuddy.ParseModelConfig("workbuddy", []byte(config), at)
		if err != nil {
			t.Fatal(err)
		}
		wants := []struct {
			rate, was *float64
			free      bool
		}{
			{new(0.29), nil, false}, {nil, nil, false}, {nil, nil, false}, {new(0.11), nil, false},
			{new(float64(0)), new(0.4), true}, {new(0.5), new(float64(2)), false}, {new(0.5), nil, false},
			{new(float64(1)), nil, false}, {new(float64(1)), nil, false}, {new(float64(1)), nil, false}, {new(float64(0)), new(float64(1)), true}, {new(0.25), new(float64(2)), false},
		}
		for i, want := range wants {
			model := models[i]
			if !reflect.DeepEqual(model.CreditRate, want.rate) || !reflect.DeepEqual(model.CreditRateWas, want.was) || model.Free == nil || *model.Free != want.free {
				t.Errorf("%s price = %#v at %v", model.ID, model, at)
			}
		}
	}
	models, err := workbuddy.ParseModelConfig("workbuddy", []byte(config), time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if models[4].CreditRate == nil || *models[4].CreditRate != 0.4 || models[4].CreditRateWas != nil || models[4].Free == nil || *models[4].Free {
		t.Fatal("expired whole-day promotion did not restore base credits")
	}
}

func TestModelCatalogSnapshotJSONKeepsKnownZeroAndNoCurrency(t *testing.T) {
	models := parseCatalog(t, "workbuddy", `{"agents":[{"name":"cli","models":["free","unknown"]}],"models":[{"id":"free","credits":0},{"id":"unknown"}]}`)
	raw, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields[0]["credit_rate"]) != "0" || string(fields[0]["free"]) != "true" || fields[1]["credit_rate"] != nil || fields[1]["free"] != nil {
		t.Fatal("snapshot JSON lost the distinction between known zero and unknown credits")
	}
	allowed := map[string]bool{"id": true, "name": true, "context_length": true, "max_output_tokens": true, "images": true, "efforts": true, "default_effort": true, "credit_rate": true, "credit_rate_was": true, "free": true}
	for _, model := range fields {
		for field := range model {
			if !allowed[field] {
				t.Errorf("unexpected persisted currency or contract field %q", field)
			}
		}
	}
	var restored []workbuddy.CatalogModel
	if err := json.Unmarshal(raw, &restored); err != nil || !reflect.DeepEqual(restored, models) {
		t.Fatalf("snapshot round trip: %v", err)
	}
}

func TestModelCatalogEmptyCLIStillAppendsEnterpriseModels(t *testing.T) {
	models := parseCatalog(t, "workbuddy", `{"agents":[{"name":"cli","models":[]}],"models":[{"id":"desktop","modelType":"custom"},{"id":"custom:a|a"},{"id":"custom:a|a"},{"id":"custom:b|b"}]}`)
	if len(models) != 2 || models[0].ID != "custom:a|a" || models[1].ID != "custom:b|b" {
		t.Fatalf("empty CLI enterprise additions = %#v", models)
	}
}

func TestModelCatalogPromotionFallbackAndDailyPrecedence(t *testing.T) {
	models := parseCatalog(t, "workbuddy-ai", `{"agents":[{"name":"cli","models":["fallback","daily","unpriced","spaces"]}],"models":[{"id":"fallback","credits":2},{"id":"daily","credits":2},{"id":"unpriced"},{"id":"spaces","credits":"x0.34\tcredits"}],"modelPromotions":[
		{"kind":"discount","modelIds":["fallback"],"discount":{"discountedCredits":"invalid","factor":0.25}},
		{"kind":"discount","modelIds":["daily"],"discount":{"factor":0}},
		{"kind":"discount","modelIds":["daily"],"schedule":{"daily":[{"start":"0:00","end":"23:59"}]}},
		{"kind":"discount","modelIds":["unpriced"],"discount":{"factor":0.5}}
	]}`)
	if models[0].CreditRate == nil || *models[0].CreditRate != 0.5 || models[0].CreditRateWas == nil || *models[0].CreditRateWas != 2 {
		t.Fatal("unreadable discounted credits did not fall back to the factor")
	}
	if models[1].CreditRate == nil || *models[1].CreditRate != 2 || models[1].CreditRateWas != nil || models[1].Free == nil || *models[1].Free {
		t.Fatal("daily promotion did not keep the full base price over other discounts")
	}
	if models[2].CreditRate != nil || models[2].CreditRateWas != nil || models[2].Free != nil {
		t.Fatal("a factor without known base credits invented a price")
	}
	if models[3].CreditRate == nil || *models[3].CreditRate != 0.34 {
		t.Fatal("credit units separated by whitespace were lost")
	}
}
