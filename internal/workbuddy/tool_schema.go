package workbuddy

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

var toolSchemaSingle = [...]string{"items", "additionalItems", "unevaluatedItems", "contains", "not", "if", "then", "else", "propertyNames", "additionalProperties", "unevaluatedProperties"}
var toolSchemaLists = [...]string{"anyOf", "oneOf", "allOf", "prefixItems"}
var toolSchemaMaps = [...]string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}

// normalizeToolSchema ports the upstream tool parameter repairs. Only schema
// positions are visited: examples, enum values, and property names are data.
func normalizeToolSchema(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, failure("invalid_request", "tool parameters must be valid JSON", 400)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, failure("invalid_request", "tool parameters must be valid JSON", 400)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	if !toolSchemaDepthOK(root, 0) {
		return nil, failure("invalid_request", "tool parameter schemas exceed the maximum depth of 64", 400)
	}
	changed := fitToolSchema(root)
	if fitToolShape(root) {
		changed = true
	}
	if typeToolUnions(root, root) {
		changed = true
	}
	if objectToolRoot(root, root, 0) {
		changed = true
	}
	if !changed {
		return raw, nil
	}
	result, err := json.Marshal(root)
	if err != nil {
		return nil, failure("invalid_request", "cannot encode tool parameters", 400)
	}
	return result, nil
}

// walkToolSchemas visits direct child schemas, leaving annotation payloads alone.
// Tuple items are also visited before fitToolSchema replaces the tuple.
func walkToolSchemas(s map[string]any, visit func(any) bool) bool {
	changed := false
	for _, key := range toolSchemaSingle {
		value, exists := s[key]
		if !exists {
			continue
		}
		if tuple, ok := value.([]any); ok && key == "items" {
			for _, child := range tuple {
				if visit(child) {
					changed = true
				}
			}
		} else if visit(value) {
			changed = true
		}
	}
	for _, key := range toolSchemaLists {
		children, _ := s[key].([]any)
		for _, child := range children {
			if visit(child) {
				changed = true
			}
		}
	}
	for _, key := range toolSchemaMaps {
		children, _ := s[key].(map[string]any)
		for _, child := range children {
			if visit(child) {
				changed = true
			}
		}
	}
	return changed
}

func toolSchemaDepthOK(value any, depth int) bool {
	if depth > 64 {
		return false
	}
	s, ok := value.(map[string]any)
	if !ok {
		return true
	}
	return !walkToolSchemas(s, func(child any) bool { return !toolSchemaDepthOK(child, depth+1) })
}

func fitToolSchema(s map[string]any) bool {
	changed := false
	if pattern, ok := s["pattern"].(string); ok {
		if repaired := toolSchemaPattern(pattern); repaired != pattern {
			s["pattern"] = repaired
			changed = true
		}
	}
	if tuple, ok := s["items"].([]any); ok {
		list := make([]any, 0, len(tuple))
		for _, item := range tuple {
			switch item.(type) {
			case map[string]any, []any:
				list = append(list, item)
			}
		}
		switch len(list) {
		case 0:
			s["items"] = map[string]any{}
		case 1:
			s["items"] = list[0]
		default:
			s["items"] = map[string]any{"anyOf": list}
		}
		delete(s, "additionalItems")
		changed = true
	}
	if walkToolSchemas(s, func(child any) bool {
		object, ok := child.(map[string]any)
		return ok && fitToolSchema(object)
	}) {
		changed = true
	}
	return changed
}

func toolSchemaPattern(pattern string) string {
	if !strings.Contains(pattern, `\0`) {
		return pattern
	}
	var out strings.Builder
	start := 0
	for i := 0; i+1 < len(pattern); i++ {
		if pattern[i] != '\\' {
			continue
		}
		if pattern[i+1] == '\\' {
			i++
			continue
		}
		if pattern[i+1] == '0' && (i+2 == len(pattern) || pattern[i+2] < '0' || pattern[i+2] > '9') {
			out.WriteString(pattern[start:i])
			out.WriteString(`\u0000`)
			i++
			start = i + 1
		}
	}
	if start == 0 {
		return pattern
	}
	out.WriteString(pattern[start:])
	return out.String()
}

func fitToolShape(s map[string]any) bool {
	changed := false
	if value, exists := s["type"]; exists {
		list, ok := value.([]any)
		if !ok {
			list = []any{value}
		}
		uniq := make([]any, 0, len(list))
		for _, item := range list {
			name, ok := item.(string)
			if !ok {
				continue
			}
			name = strings.ToLower(name)
			switch name {
			case "string", "number", "integer", "boolean", "object", "array", "null":
			default:
				continue
			}
			if !toolSchemaIncludes(uniq, name) {
				uniq = append(uniq, name)
			}
		}
		var want any
		if len(uniq) == 1 {
			want = uniq[0]
		} else if len(uniq) > 1 {
			want = uniq
		}
		if want == nil || !reflect.DeepEqual(want, value) {
			if want == nil {
				delete(s, "type")
			} else {
				s["type"] = want
			}
			changed = true
		}
	}
	if value, exists := s["required"]; exists {
		if list, ok := value.([]any); ok {
			var filtered []any
			for i, item := range list {
				if _, ok := item.(string); !ok && filtered == nil {
					filtered = append(make([]any, 0, len(list)), list[:i]...)
					changed = true
				} else if ok && filtered != nil {
					filtered = append(filtered, item)
				}
			}
			if filtered != nil {
				s["required"] = filtered
			}
		} else {
			delete(s, "required")
			changed = true
		}
	}
	if fitToolEnum(s) {
		changed = true
	}
	fit := func(value any, key string) any {
		if flag, ok := value.(bool); ok && key != "additionalProperties" && key != "unevaluatedProperties" {
			changed = true
			if flag {
				value = map[string]any{}
			} else {
				value = map[string]any{"not": map[string]any{}}
			}
		}
		if object, ok := value.(map[string]any); ok && fitToolShape(object) {
			changed = true
		}
		return value
	}
	for _, key := range toolSchemaSingle {
		if value, exists := s[key]; exists {
			s[key] = fit(value, key)
		}
	}
	for _, key := range toolSchemaLists {
		children, _ := s[key].([]any)
		for i, child := range children {
			children[i] = fit(child, key)
		}
	}
	for _, key := range toolSchemaMaps {
		children, _ := s[key].(map[string]any)
		for name, child := range children {
			children[name] = fit(child, key)
		}
	}
	return changed
}

func toolSchemaIncludes(values []any, want string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == want {
			return true
		}
	}
	return false
}

func toolSchemaValueType(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case bool:
		return "boolean"
	case json.Number:
		if !strings.ContainsAny(string(value), ".eE") {
			return "integer"
		}
		if toolSchemaNumberIsInteger(string(value)) {
			return "integer"
		}
		return "number"
	default:
		return "string"
	}
}

func fitToolEnum(s map[string]any) bool {
	value, exists := s["enum"]
	if !exists {
		return false
	}
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		delete(s, "enum")
		return true
	}
	listed, ok := s["type"].([]any)
	if !ok {
		if name, ok := s["type"].(string); ok {
			listed = []any{name}
		}
	}
	nonNull := 0
	for _, name := range listed {
		if name != "null" {
			nonNull++
		}
	}
	if nonNull == 1 {
		return false
	}
	groups := make(map[string][]any)
	order := make([]string, 0, 7)
	for _, value := range values {
		kind := toolSchemaValueType(value)
		if _, exists := groups[kind]; !exists {
			order = append(order, kind)
		}
		groups[kind] = append(groups[kind], value)
	}
	if integers, exists := groups["integer"]; exists && groups["number"] != nil {
		groups["number"] = append(groups["number"], integers...)
		delete(groups, "integer")
	}
	_, hasNull := groups["null"]
	delete(groups, "null")
	if len(groups) <= 1 {
		var want any = "null"
		for kind := range groups {
			want = kind
			if hasNull {
				want = []any{kind, "null"}
			}
		}
		if reflect.DeepEqual(want, s["type"]) {
			return false
		}
		s["type"] = want
		return true
	}
	split := make([]any, 0, len(groups)+1)
	for _, kind := range order {
		if values, exists := groups[kind]; exists {
			split = append(split, map[string]any{"type": kind, "enum": values})
		}
	}
	if hasNull {
		split = append(split, map[string]any{"type": "null"})
	}
	delete(s, "enum")
	delete(s, "type")
	if _, exists := s["anyOf"].([]any); exists {
		all, _ := s["allOf"].([]any)
		s["allOf"] = append(all, map[string]any{"anyOf": split})
	} else {
		s["anyOf"] = split
	}
	return true
}

func toolSchemaNumberIsInteger(number string) bool {
	mantissa, exponent := number, "0"
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		mantissa, exponent = number[:i], number[i+1:]
	}
	fractional, trailingZeros := 0, 0
	afterDot, nonzero := false, false
	for _, digit := range mantissa {
		if digit == '.' {
			afterDot = true
			continue
		}
		if digit < '0' || digit > '9' {
			continue
		}
		if afterDot {
			fractional++
		}
		if digit == '0' {
			trailingZeros++
		} else {
			nonzero = true
			trailingZeros = 0
		}
	}
	if !nonzero {
		return true
	}
	power, err := strconv.ParseInt(exponent, 10, 64)
	if err != nil {
		return !strings.HasPrefix(exponent, "-")
	}
	return power >= int64(fractional-trailingZeros)
}

func localToolRef(root map[string]any, s map[string]any) map[string]any {
	ref, ok := s["$ref"].(string)
	if !ok || !strings.HasPrefix(ref, "#/") {
		return s
	}
	var at any = root
	for segment := range strings.SplitSeq(ref[2:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch value := at.(type) {
		case map[string]any:
			at = value[key]
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(value) || strconv.Itoa(index) != key {
				return s
			}
			at = value[index]
		default:
			return s
		}
	}
	if target, ok := at.(map[string]any); ok {
		return target
	}
	return s
}

func typeToolUnions(s, root map[string]any) bool {
	changed := false
	if _, typed := s["type"]; !typed {
		if branches, ok := s["oneOf"].([]any); ok && len(branches) > 0 {
			allObjects := true
			for _, branch := range branches {
				if !toolObjectBranch(root, branch, nil) {
					allObjects = false
					break
				}
			}
			if allObjects {
				s["type"] = "object"
				changed = true
			}
		}
	}
	if walkToolSchemas(s, func(value any) bool {
		child, ok := value.(map[string]any)
		return ok && typeToolUnions(child, root)
	}) {
		changed = true
	}
	return changed
}

func toolObjectBranch(root map[string]any, value any, seen []string) bool {
	branch, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if kind, typed := branch["type"]; typed {
		text, ok := kind.(string)
		return ok && text == "object"
	}
	ref, ok := branch["$ref"].(string)
	if !ok || len(seen) > 16 {
		return false
	}
	if slices.Contains(seen, ref) {
		return false
	}
	if !strings.HasPrefix(ref, "#/") {
		return false
	}
	return toolObjectBranch(root, localToolRef(root, branch), append(seen, ref))
}

func toolSchemaHasUnion(s map[string]any) bool {
	for _, key := range [...]string{"allOf", "anyOf", "oneOf"} {
		if _, ok := s[key].([]any); ok {
			return true
		}
	}
	return false
}

func objectToolRoot(s, root map[string]any, depth int) bool {
	if s["type"] == "object" && !toolSchemaHasUnion(s) {
		return false
	}
	props, _ := s["properties"].(map[string]any)
	if props == nil {
		props = make(map[string]any)
	}
	required, _ := s["required"].([]any)
	required = append([]any(nil), required...)
	branchObject := func(value any) map[string]any {
		branch, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		branch = localToolRef(root, branch)
		if depth < 8 && toolSchemaHasUnion(branch) {
			// Flatten a copy: a definition used as a root branch remains intact.
			copy := make(map[string]any, len(branch))
			maps.Copy(copy, branch)
			if original, ok := branch["properties"].(map[string]any); ok {
				properties := make(map[string]any, len(original))
				maps.Copy(properties, original)
				copy["properties"] = properties
			}
			branch = copy
			objectToolRoot(branch, root, depth+1)
		}
		return branch
	}
	all, _ := s["allOf"].([]any)
	for _, value := range all {
		branch := branchObject(value)
		fields, _ := branch["properties"].(map[string]any)
		for key, field := range fields {
			if _, exists := props[key]; !exists {
				props[key] = field
			}
		}
		if list, ok := branch["required"].([]any); ok {
			required = append(required, list...)
		}
	}
	var branches []map[string]any
	for _, key := range [...]string{"anyOf", "oneOf"} {
		list, _ := s[key].([]any)
		for _, value := range list {
			branch := branchObject(value)
			if branch == nil {
				continue
			}
			_, properties := branch["properties"].(map[string]any)
			if branch["type"] == "object" || properties {
				branches = append(branches, branch)
			}
		}
	}
	from := make(map[string][]any)
	for _, branch := range branches {
		fields, _ := branch["properties"].(map[string]any)
		for key, field := range fields {
			if _, exists := props[key]; exists {
				continue
			}
			duplicate := false
			for _, previous := range from[key] {
				if reflect.DeepEqual(previous, field) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				from[key] = append(from[key], field)
			}
		}
	}
	for key, list := range from {
		if len(list) == 1 {
			props[key] = list[0]
		} else {
			props[key] = map[string]any{"anyOf": list}
		}
	}
	if len(branches) > 0 {
		// Required order has no schema meaning; retain the first branch's list
		// order while finding fields present and required in every object branch.
		first, _ := branches[0]["required"].([]any)
		firstProps, _ := branches[0]["properties"].(map[string]any)
		for _, value := range first {
			key, ok := value.(string)
			if _, exists := firstProps[key]; !ok || !exists {
				continue
			}
			common := true
			for _, branch := range branches {
				list, _ := branch["required"].([]any)
				if !toolSchemaIncludes(list, key) {
					common = false
					break
				}
			}
			if common {
				required = append(required, key)
			}
		}
	}
	for _, key := range [...]string{"allOf", "anyOf", "oneOf"} {
		if _, ok := s[key].([]any); ok {
			delete(s, key)
		}
	}
	s["type"] = "object"
	s["properties"] = props
	unique := make([]any, 0, len(required))
	for _, value := range required {
		key, ok := value.(string)
		if _, exists := props[key]; ok && exists && !toolSchemaIncludes(unique, key) {
			unique = append(unique, key)
		}
	}
	if len(unique) > 0 {
		s["required"] = unique
	} else {
		delete(s, "required")
	}
	return true
}
