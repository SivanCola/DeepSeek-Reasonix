package sessionstore

import (
	"encoding/json"
	"strings"
)

type sessionDAGLocations struct {
	message sessionDAGPayload
	targets map[string]sessionDAGPayload
}

func locateSessionDAGPayloads(raw []byte, start int64) (sessionDAGLocations, error) {
	var locations sessionDAGLocations
	err := sessionObjectFields(raw, 0, func(key string, begin, end int) error {
		switch {
		case strings.EqualFold(key, "msgs"):
			locations.message = sessionDAGPayload{start: start + int64(begin), end: start + int64(end)}
		case strings.EqualFold(key, "targets"):
			if raw[begin] == 'n' {
				locations.targets = nil
				return nil
			}
			if locations.targets == nil {
				locations.targets = make(map[string]sessionDAGPayload)
			}
			return sessionObjectFields(raw, begin, func(id string, begin, end int) error {
				locations.targets[id] = sessionDAGPayload{start: start + int64(begin), end: start + int64(end)}
				return nil
			})
		}
		return nil
	})
	return locations, err
}

// sessionObjectFields visits already validated JSON without copying values.
func sessionObjectFields(raw []byte, offset int, visit func(string, int, int) error) error {
	for offset++; ; {
		offset = sessionJSONSpace(raw, offset)
		if raw[offset] == '}' {
			return nil
		}
		keyEnd := preflightSessionEventValue(raw, offset)
		var key string
		if err := json.Unmarshal(raw[offset:keyEnd], &key); err != nil {
			return err
		}
		offset = sessionJSONSpace(raw, keyEnd) + 1
		begin := sessionJSONSpace(raw, offset)
		end := sessionJSONValueEnd(raw, begin)
		if err := visit(key, begin, end); err != nil {
			return err
		}
		offset = sessionJSONSpace(raw, end)
		if raw[offset] == '}' {
			return nil
		}
		offset++
	}
}

func sessionJSONSpace(raw []byte, offset int) int {
	for raw[offset] == ' ' || raw[offset] == '\n' || raw[offset] == '\r' || raw[offset] == '\t' {
		offset++
	}
	return offset
}

func sessionJSONValueEnd(raw []byte, offset int) int {
	if raw[offset] != '{' && raw[offset] != '[' {
		return preflightSessionEventValue(raw, offset)
	}
	depth := 1
	for offset++; depth != 0; {
		switch raw[offset] {
		case '"':
			offset = preflightSessionEventValue(raw, offset)
			continue
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
		offset++
	}
	return offset
}
