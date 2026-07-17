package hashline

import (
	"encoding/json"
	"fmt"
)

// ParseEdits accepts an edits value that may be:
//   - a JSON array of ops
//   - a single op object
//   - a double-JSON-encoded string of either form
func ParseEdits(raw json.RawMessage) ([]Op, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("edits is required")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("invalid edits: %w", err)
	}
	return decodeEditsValue(v)
}

func decodeEditsValue(v any) ([]Op, error) {
	switch t := v.(type) {
	case string:
		var inner any
		if err := json.Unmarshal([]byte(t), &inner); err != nil {
			return nil, fmt.Errorf("edits was a JSON string but could not be parsed as an array of operations: %w", err)
		}
		return decodeEditsValue(inner)
	case []any:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		var ops []Op
		if err := json.Unmarshal(b, &ops); err != nil {
			return nil, fmt.Errorf("invalid edits array: %w", err)
		}
		for i := range ops {
			if ops[i].Kind == "" {
				return nil, fmt.Errorf("edits[%d]: op is required", i)
			}
		}
		return ops, nil
	case map[string]any:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		var op Op
		if err := json.Unmarshal(b, &op); err != nil {
			return nil, fmt.Errorf("invalid edit object: %w", err)
		}
		if op.Kind == "" {
			return nil, fmt.Errorf("edits: op is required")
		}
		return []Op{op}, nil
	default:
		return nil, fmt.Errorf("edits must be an array of edit operations")
	}
}
