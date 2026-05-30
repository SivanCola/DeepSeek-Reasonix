package plugin

import (
	"encoding/json"
	"sort"

	"reasonix/internal/tool"
)

// sortToolsByName returns a new slice of tools sorted alphabetically by Name().
func sortToolsByName(tools []tool.Tool) []tool.Tool {
	sorted := make([]tool.Tool, len(tools))
	copy(sorted, tools)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Name() < sorted[j].Name()
	})
	return sorted
}

// canonicalizeSchema recursively stabilizes a JSON Schema so the same logical
// schema always produces the same byte representation — important for cache
// fingerprint stability across MCP sessions.
func canonicalizeSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw // not valid JSON — pass through unchanged
	}
	canon := canonicalizeValue(v)
	b, err := json.Marshal(canon)
	if err != nil {
		return raw
	}
	return json.RawMessage(b)
}

// setLikeArrays lists the JSON Schema property names whose arrays are sets
// (order does not affect validation meaning).
var setLikeArrays = map[string]bool{
	"required":           true,
	"dependentRequired": true,
}

// orderedArrays lists the JSON Schema property names whose arrays have
// position-dependent meaning — their original order must be preserved.
var orderedArrays = map[string]bool{
	"enum":    true,
	"oneOf":   true,
	"anyOf":   true,
	"allOf":   true,
	"any_of":  true,
	"one_of":  true,
	"all_of":  true,
	"type":    true,
}

func canonicalizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		// Process inner values first.
		for k, inner := range val {
			val[k] = canonicalizeValue(inner)
		}
		// Sort arrays that are set-like.
		for key := range val {
			if setLikeArrays[key] {
				if arr, ok := val[key].([]any); ok {
					sort.SliceStable(arr, func(i, j int) bool {
						return jsonString(arr[i]) < jsonString(arr[j])
					})
				}
			}
		}
		// Sort dependentRequired inner arrays (each value is an array of strings).
		if dr, ok := val["dependentRequired"]; ok {
			if drMap, ok := dr.(map[string]any); ok {
				for _, inner := range drMap {
					if arr, ok := inner.([]any); ok {
						sort.SliceStable(arr, func(i, j int) bool {
							return jsonString(arr[i]) < jsonString(arr[j])
						})
					}
				}
			}
		}
		return val
	case []any:
		for i, elem := range val {
			val[i] = canonicalizeValue(elem)
		}
		return val
	default:
		return v
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
