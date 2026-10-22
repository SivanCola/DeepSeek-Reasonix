package sessionstore

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func preflightSessionEventMessages(
	path string,
	raw []byte,
	existingMessages, existingCollectionItems int,
	limits sessionReplayLimits,
) (messageCount, collectionItems int, err error) {
	if err := limits.err(); err != nil {
		return 0, existingCollectionItems, err
	}
	if !json.Valid(raw) {
		// Unmarshal checks syntax before decoding, including on this error path.
		var value struct{}
		return 0, existingCollectionItems, json.Unmarshal(raw, &value)
	}
	raw = bytes.TrimSpace(raw)
	if raw[0] != '[' {
		return 0, existingCollectionItems, fmt.Errorf("messages must be an array")
	}

	collectionItems = existingCollectionItems
	var storage [64]byte
	containers := storage[:0]
	for offset := 0; offset < len(raw); {
		if err := limits.err(); err != nil {
			return 0, existingCollectionItems, err
		}
		switch raw[offset] {
		case ' ', '\t', '\r', '\n', ',', ':':
			offset++
			continue
		case ']', '}':
			containers = containers[:len(containers)-1]
			offset++
			continue
		}
		if len(containers) > 0 && containers[len(containers)-1] == '[' {
			if len(containers) == 1 {
				if existingMessages+messageCount >= limits.maxMessages {
					return 0, existingCollectionItems, sessionReplayLimitError(
						path, "messages", int64(existingMessages+messageCount+1), int64(limits.maxMessages),
					)
				}
				messageCount++
			} else {
				if collectionItems >= limits.maxCollectionItems {
					return 0, existingCollectionItems, sessionReplayLimitError(
						path, "message_collection_items", int64(collectionItems+1), int64(limits.maxCollectionItems),
					)
				}
				collectionItems++
			}
		}
		if raw[offset] == '[' || raw[offset] == '{' {
			containers = append(containers, raw[offset])
			offset++
		} else {
			offset = preflightSessionEventValue(raw, offset)
		}
	}
	return messageCount, collectionItems, nil
}

// preflightSessionEventValue skips a validated scalar without decoding strings.
func preflightSessionEventValue(raw []byte, offset int) int {
	if raw[offset] == '"' {
		for offset++; ; offset++ {
			switch raw[offset] {
			case '\\':
				offset++
			case '"':
				return offset + 1
			}
		}
	}
	for offset < len(raw) {
		switch raw[offset] {
		case ' ', '\t', '\r', '\n', ',', ']', '}':
			return offset
		}
		offset++
	}
	return offset
}
