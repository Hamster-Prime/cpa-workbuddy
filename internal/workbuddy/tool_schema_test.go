package workbuddy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"cpa-workbuddy/internal/workbuddy"
)

func schemaProtocolParameters(t *testing.T, parameters string) json.RawMessage {
	t.Helper()
	s := workbuddy.New()
	defer s.Close()
	auth := parsedIdentity(t, "workbuddy", "")
	field := ""
	if parameters != "" {
		field = `,"parameters":` + parameters
	}
	payload := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"probe","description":"d"` + field + `}}]}`)
	body := requestProtocolBody(t, executeRequestProtocol(t, s, requestProtocolInput(auth, "wb/glm-5.3", payload)))
	var tools []struct {
		Function struct {
			Parameters json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(body["tools"], &tools); err != nil || len(tools) != 1 {
		t.Fatalf("upstream tools = %s, error %v", body["tools"], err)
	}
	return tools[0].Function.Parameters
}

func requireSchemaJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	decode := func(raw []byte) any {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("invalid schema JSON %s: %v", raw, err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(got), decode([]byte(want))) {
		t.Fatalf("upstream parameters = %s\nwant %s", got, want)
	}
}

func TestToolSchemaRootParameters(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"missing", ``, `{"type":"object","properties":{}}`},
		{"null", `null`, `{"type":"object","properties":{}}`},
		{"false", `false`, `{"type":"object","properties":{}}`},
		{"true", `true`, `{"type":"object","properties":{}}`},
		{"array", `[{"type":"string"}]`, `{"type":"object","properties":{}}`},
		{"string", `"text"`, `{"type":"object","properties":{}}`},
		{"number", `9007199254740993`, `{"type":"object","properties":{}}`},
		{"empty", `{}`, `{"type":"object","properties":{}}`},
		{"untyped", `{"properties":{"x":{"type":"string"}}}`, `{"type":"object","properties":{"x":{"type":"string"}}}`},
		{"scalar-type", `{"type":"string","description":"kept"}`, `{"type":"object","properties":{},"description":"kept"}`},
		{"anyOf", `{"anyOf":[{"type":"object","properties":{"kind":{"const":"a"},"x":{"type":"string"}},"required":["kind","x"]},{"type":"object","properties":{"kind":{"const":"b"},"y":{"type":"number"}},"required":["kind"]}]}`, `{"type":"object","properties":{"kind":{"anyOf":[{"const":"a"},{"const":"b"}]},"x":{"type":"string"},"y":{"type":"number"}},"required":["kind"]}`},
		{"allOf-ref", `{"$defs":{"A":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}},"allOf":[{"$ref":"#/$defs/A"},{"type":"object","properties":{"b":{"type":"string"}}}]}`, `{"$defs":{"A":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}},"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a"]}`},
		{"escaped-ref", `{"definitions":{"A/B~C":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}},"oneOf":[{"$ref":"#/definitions/A~1B~0C"},{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}},"required":["a","b"]}]}`, `{"definitions":{"A/B~C":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}},"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}},"required":["a"]}`},
		{"root-wins", `{"properties":{"x":{"type":"boolean"}},"required":["x","missing","x"],"allOf":[{"properties":{"x":{"type":"string"},"y":{"type":"number"}},"required":["y","missing"]}],"anyOf":[{"type":"object","properties":{"x":{"type":"integer"},"z":{"type":"string"}},"required":["z"]},{"type":"string"}]}`, `{"type":"object","properties":{"x":{"type":"boolean"},"y":{"type":"number"},"z":{"type":"string"}},"required":["x","y","z"]}`},
		{"nested-root-union", `{"$defs":{"A":{"allOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}]}},"allOf":[{"$ref":"#/$defs/A"}]}`, `{"$defs":{"A":{"allOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}]}},"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`},
		{"root-ref-cycle", `{"$defs":{"A":{"allOf":[{"$ref":"#/$defs/A"}]}},"allOf":[{"$ref":"#/$defs/A"}]}`, `{"$defs":{"A":{"allOf":[{"$ref":"#/$defs/A"}]}},"type":"object","properties":{}}`},
		{"root-direct-ref-cycle", `{"$defs":{"A":{"$ref":"#/$defs/B"},"B":{"$ref":"#/$defs/A"}},"$ref":"#/$defs/A"}`, `{"$defs":{"A":{"$ref":"#/$defs/B"},"B":{"$ref":"#/$defs/A"}},"$ref":"#/$defs/A","type":"object","properties":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireSchemaJSON(t, schemaProtocolParameters(t, tc.input), tc.want)
		})
	}
}

func TestToolSchemaPropertyRepairs(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"pattern", `{"type":"string","pattern":"^[^\\0]*$"}`, `{"type":"string","pattern":"^[^\\u0000]*$"}`},
		{"nested-pattern", `{"type":"array","items":{"type":"string","pattern":"x\\0"}}`, `{"type":"array","items":{"type":"string","pattern":"x\\u0000"}}`},
		{"escaped-pattern", `{"type":"string","pattern":"\\\\0|\\01|\\09|\\0x|\\\\\\0"}`, `{"type":"string","pattern":"\\\\0|\\01|\\09|\\u0000x|\\\\\\u0000"}`},
		{"tuple", `{"type":"array","items":[{"type":"number"},{"type":"string"}],"additionalItems":false}`, `{"type":"array","items":{"anyOf":[{"type":"number"},{"type":"string"}]}}`},
		{"single-tuple", `{"type":"array","items":[{"type":"number"}]}`, `{"type":"array","items":{"type":"number"}}`},
		{"empty-tuple", `{"type":"array","items":[],"additionalItems":{"type":"string"}}`, `{"type":"array","items":{}}`},
		{"tuple-nonobjects", `{"type":"array","items":[true,false,null,1,"x",{"enum":["a"]}]}`, `{"type":"array","items":{"enum":["a"],"type":"string"}}`},
		{"enum-string", `{"enum":["x","y"],"description":"d"}`, `{"enum":["x","y"],"description":"d","type":"string"}`},
		{"enum-integer", `{"enum":[1,2]}`, `{"enum":[1,2],"type":"integer"}`},
		{"enum-number", `{"enum":[1,2.5]}`, `{"enum":[1,2.5],"type":"number"}`},
		{"enum-nullable", `{"enum":[true,null]}`, `{"enum":[true,null],"type":["boolean","null"]}`},
		{"enum-null", `{"enum":[null]}`, `{"enum":[null],"type":"null"}`},
		{"enum-item", `{"type":"array","items":{"enum":["a"]}}`, `{"type":"array","items":{"enum":["a"],"type":"string"}}`},
		{"enum-anyOf", `{"anyOf":[{"enum":["a"]},{"type":"integer"}]}`, `{"anyOf":[{"enum":["a"],"type":"string"},{"type":"integer"}]}`},
		{"mixed-enum", `{"enum":["x",1,null],"description":"d"}`, `{"description":"d","anyOf":[{"type":"string","enum":["x"]},{"type":"integer","enum":[1]},{"type":"null"}]}`},
		{"mixed-typed-enum", `{"type":["string","integer","null"],"enum":["x",1,null]}`, `{"anyOf":[{"type":"string","enum":["x"]},{"type":"integer","enum":[1]},{"type":"null"}]}`},
		{"mixed-enum-existing-union", `{"anyOf":[{"minLength":1}],"allOf":[{"maxLength":4}],"enum":["x",1]}`, `{"anyOf":[{"minLength":1}],"allOf":[{"maxLength":4},{"anyOf":[{"type":"string","enum":["x"]},{"type":"integer","enum":[1]}]}]}`},
		{"number-enum-group-order", `{"enum":["x",1,2.5,2,null]}`, `{"anyOf":[{"type":"string","enum":["x"]},{"type":"number","enum":[2.5,1,2]},{"type":"null"}]}`},
		{"object-array-enum", `{"enum":[{},[],true]}`, `{"anyOf":[{"type":"object","enum":[{}]},{"type":"array","enum":[[]]},{"type":"boolean","enum":[true]}]}`},
		{"typed-enum", `{"type":"string","enum":["a"]}`, `{"type":"string","enum":["a"]}`},
		{"typed-nullable-enum", `{"type":["integer","null"],"enum":[1,null]}`, `{"type":["integer","null"],"enum":[1,null]}`},
		{"typed-null-first-enum", `{"type":["null","string"],"enum":[null,"a"]}`, `{"type":["null","string"],"enum":[null,"a"]}`},
		{"typed-null-enum", `{"type":"null","enum":[null]}`, `{"type":"null","enum":[null]}`},
		{"typed-inconsistent-enum", `{"type":"string","enum":[1]}`, `{"type":"string","enum":[1]}`},
		{"empty-enum", `{"type":"string","enum":[]}`, `{"type":"string"}`},
		{"nonarray-enum", `{"enum":"a"}`, `{}`},
		{"required-string", `{"type":"object","properties":{"x":{"type":"string"}},"required":"x"}`, `{"type":"object","properties":{"x":{"type":"string"}}}`},
		{"required-filter", `{"type":"object","required":[false,"x",null,"y",1]}`, `{"type":"object","required":["x","y"]}`},
		{"required-empty", `{"type":"object","required":[false,null]}`, `{"type":"object","required":[]}`},
		{"type-case", `{"type":"String"}`, `{"type":"string"}`},
		{"type-unknown", `{"type":"any"}`, `{}`},
		{"type-filter", `{"type":["Integer","foo","null"]}`, `{"type":["integer","null"]}`},
		{"type-deduplicate", `{"type":["String","string",false,"STRING"]}`, `{"type":"string"}`},
		{"true-schema", `true`, `{}`},
		{"false-schema", `false`, `{"not":{}}`},
		{"true-items", `{"type":"array","items":true}`, `{"type":"array","items":{}}`},
		{"bool-additional", `{"type":"object","additionalProperties":true,"unevaluatedProperties":false}`, `{"type":"object","additionalProperties":true,"unevaluatedProperties":false}`},
		{"large-number", `{"enum":[9007199254740993],"minimum":9007199254740993}`, `{"enum":[9007199254740993],"minimum":9007199254740993,"type":"integer"}`},
		{"large-fraction", `{"enum":[9007199254740993.1]}`, `{"enum":[9007199254740993.1],"type":"number"}`},
		{"integer-exponent", `{"enum":[10.0e-1,2e3,0.00e-999999999999999999999]}`, `{"enum":[10.0e-1,2e3,0.00e-999999999999999999999],"type":"integer"}`},
		{"fraction-exponent", `{"enum":[10e-2]}`, `{"enum":[10e-2],"type":"number"}`},
		{"huge-exponent", `{"enum":[1e999999999999999999999]}`, `{"enum":[1e999999999999999999999],"type":"integer"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"type":"object","properties":{"a":` + tc.input + `}}`
			want := `{"type":"object","properties":{"a":` + tc.want + `}}`
			requireSchemaJSON(t, schemaProtocolParameters(t, input), want)
		})
	}
}

func TestToolSchemaTraversesSchemaPositionsOnly(t *testing.T) {
	input := `{"type":"object","properties":{"enum":{"type":"string"},"type":{"type":"string","enum":["a"]},"required":{"type":"boolean"},"pattern":{"type":"string"},"items":{"type":"boolean"}},"required":["enum"],"examples":[{"type":"String","enum":[],"pattern":"\\0","items":[{"type":"any"}],"oneOf":[{"type":"object"}]}],"default":{"type":"any","pattern":"\\0"},"const":{"required":"x","pattern":"\\0"},"x-extension":{"type":"any","pattern":"\\0"}}`
	if got := string(schemaProtocolParameters(t, input)); got != input {
		t.Fatalf("clean schema bytes changed:\n%s\nwant:\n%s", got, input)
	}
	input = `{"type":"object","properties":{"a":{"enum":[{"type":"any","pattern":"\\0","items":[true]}]}},"$defs":{"x":{"enum":["a"]}}}`
	want := `{"type":"object","properties":{"a":{"enum":[{"type":"any","pattern":"\\0","items":[true]}],"type":"object"}},"$defs":{"x":{"enum":["a"],"type":"string"}}}`
	requireSchemaJSON(t, schemaProtocolParameters(t, input), want)
	for _, key := range []string{"additionalItems", "unevaluatedItems", "contains", "not", "if", "then", "else", "propertyNames", "additionalProperties", "unevaluatedProperties"} {
		t.Run(key, func(t *testing.T) {
			input := `{"type":"object","` + key + `":{"enum":["a"],"pattern":"\\0"}}`
			want := `{"type":"object","` + key + `":{"enum":["a"],"type":"string","pattern":"\\u0000"}}`
			requireSchemaJSON(t, schemaProtocolParameters(t, input), want)
		})
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		t.Run(key, func(t *testing.T) {
			input := `{"type":"object","properties":{"x":{"` + key + `":[true,false,{"enum":["a"],"pattern":"\\0"}]}}}`
			want := `{"type":"object","properties":{"x":{"` + key + `":[{},{"not":{}},{"enum":["a"],"type":"string","pattern":"\\u0000"}]}}}`
			requireSchemaJSON(t, schemaProtocolParameters(t, input), want)
		})
	}
	for _, key := range []string{"patternProperties", "$defs", "definitions", "dependentSchemas"} {
		t.Run(key, func(t *testing.T) {
			input := `{"type":"object","` + key + `":{"a":true,"b":false,"enum":{"enum":["a"],"pattern":"\\0"}}}`
			want := `{"type":"object","` + key + `":{"a":{},"b":{"not":{}},"enum":{"enum":["a"],"type":"string","pattern":"\\u0000"}}}`
			requireSchemaJSON(t, schemaProtocolParameters(t, input), want)
		})
	}
}

func TestToolSchemaNestedObjectUnionsAndReferences(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"definition-oneOf", `{"type":"object","properties":{"x":{"$ref":"#/$defs/X"}},"$defs":{"X":{"oneOf":[{"type":"object","properties":{"mode":{"type":"string","const":"a"}}},{"type":"object","properties":{"mode":{"type":"string","const":"b"}}}]}}}`, `{"type":"object","properties":{"x":{"$ref":"#/$defs/X"}},"$defs":{"X":{"type":"object","oneOf":[{"type":"object","properties":{"mode":{"type":"string","const":"a"}}},{"type":"object","properties":{"mode":{"type":"string","const":"b"}}}]}}}`},
		{"nested-definition-oneOf", `{"type":"object","properties":{"x":{"$ref":"#/$defs/X"}},"$defs":{"X":{"type":"object","properties":{"y":{"oneOf":[{"type":"object","properties":{"mode":{"type":"string","const":"a"}}},{"type":"object","properties":{"mode":{"type":"string","const":"b"}}}]}},"required":["y"]}}}`, `{"type":"object","properties":{"x":{"$ref":"#/$defs/X"}},"$defs":{"X":{"type":"object","properties":{"y":{"type":"object","oneOf":[{"type":"object","properties":{"mode":{"type":"string","const":"a"}}},{"type":"object","properties":{"mode":{"type":"string","const":"b"}}}]}},"required":["y"]}}}`},
		{"reference-chain", `{"type":"object","properties":{"s":{"$ref":"#/$defs/__schema0"}},"$defs":{"__schema0":{"oneOf":[{"$ref":"#/$defs/__schema1"},{"$ref":"#/$defs/__schema3"}]},"__schema1":{"type":"object","properties":{"launch":{"type":"boolean"}}},"__schema2":{"$ref":"#/$defs/__schema1"},"__schema3":{"$ref":"#/$defs/__schema4"},"__schema4":{"type":"object","properties":{"connect":{"type":"string"}}}}}`, `{"type":"object","properties":{"s":{"$ref":"#/$defs/__schema0"}},"$defs":{"__schema0":{"type":"object","oneOf":[{"$ref":"#/$defs/__schema1"},{"$ref":"#/$defs/__schema3"}]},"__schema1":{"type":"object","properties":{"launch":{"type":"boolean"}}},"__schema2":{"$ref":"#/$defs/__schema1"},"__schema3":{"$ref":"#/$defs/__schema4"},"__schema4":{"type":"object","properties":{"connect":{"type":"string"}}}}}`},
		{"escaped-object-ref", `{"type":"object","properties":{"x":{"oneOf":[{"$ref":"#/$defs/A~1B~0C"},{"type":"object"}]}},"$defs":{"A/B~C":{"type":"object"}}}`, `{"type":"object","properties":{"x":{"type":"object","oneOf":[{"$ref":"#/$defs/A~1B~0C"},{"type":"object"}]}},"$defs":{"A/B~C":{"type":"object"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireSchemaJSON(t, schemaProtocolParameters(t, tc.input), tc.want)
		})
	}
	untouched := `{"type":"object","properties":{"typed":{"type":"object","oneOf":[{"type":"object"},{"type":"object"}]},"mixed":{"oneOf":[{"type":"object"},{"type":"string"}]},"any":{"anyOf":[{"type":"object"},{"type":"object"}]},"missing":{"oneOf":[{"type":"object"},{"$ref":"#/$defs/Nope"}]},"external":{"oneOf":[{"type":"object"},{"$ref":"other.json#/X"}]},"loop":{"oneOf":[{"type":"object"},{"$ref":"#/$defs/A"}]},"examples":{"type":"array","examples":[{"oneOf":[{"type":"object"},{"type":"object"}]}]}},"$defs":{"A":{"$ref":"#/$defs/B"},"B":{"$ref":"#/$defs/A"}}}`
	if got := string(schemaProtocolParameters(t, untouched)); got != untouched {
		t.Fatalf("unresolved or typed unions changed: %s", got)
	}
}

func TestToolSchemaPreservesCleanBytesAndNumbers(t *testing.T) {
	clean := `{"type":"object","properties":{"code":{"type":"string","pattern":"^x","description":"\\u0061"}},"required":["code"],"minimum":9007199254740993,"maximum":1.2300e+40}`
	if got := string(schemaProtocolParameters(t, clean)); got != clean {
		t.Fatalf("clean schema bytes changed: %s\nwant %s", got, clean)
	}
}

func TestToolSchemaDepthIsBoundedAtThePublicSeam(t *testing.T) {
	nested := func(depth int, leaf string) string {
		return strings.Repeat(`{"type":"object","properties":{"next":`, depth) + leaf + strings.Repeat(`}}`, depth)
	}
	t.Run("depth-64-repaired", func(t *testing.T) {
		input := nested(64, `{"type":"string","pattern":"\\0"}`)
		want := nested(64, `{"type":"string","pattern":"\\u0000"}`)
		requireSchemaJSON(t, schemaProtocolParameters(t, input), want)
	})
	for _, leaf := range []string{`{"type":"string","pattern":"\\0"}`, `true`, `false`} {
		t.Run("depth-65-"+leaf, func(t *testing.T) {
			s := workbuddy.New()
			defer s.Close()
			auth := parsedIdentity(t, "workbuddy", "")
			payload := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"probe","parameters":` + nested(65, leaf) + `}}]}`)
			_, err := invoke(s, context.Background(), "executor.execute", requestProtocolInput(auth, "wb/glm-5.3", payload), func(string, []byte) ([]byte, error) {
				t.Error("excessive schema depth reached host HTTP")
				return nil, errors.New("unexpected HTTP")
			})
			var safe *workbuddy.Error
			if !errors.As(err, &safe) || safe.Code != "invalid_request" || safe.HTTPStatus != 400 {
				t.Fatalf("depth error = %v, want invalid_request/400", err)
			}
		})
	}
	t.Run("deep-examples-are-data", func(t *testing.T) {
		input := `{"type":"object","examples":[` + nested(70, `{"type":"String","pattern":"\\0"}`) + `]}`
		if got := string(schemaProtocolParameters(t, input)); got != input {
			t.Fatalf("deep example changed: %s", got)
		}
	})
}
