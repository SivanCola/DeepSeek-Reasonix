package workspacestate

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"reasonix/internal/pathidentity"
)

// FindWorkspace admits the read-only path only for an unambiguous physical
// owner. Missing or duplicate roots go through the registration transaction.
func FindWorkspace(state State, root string) (string, bool) {
	if strings.TrimSpace(root) == "" {
		return "", false
	}
	identity, err := pathidentity.Resolve(root, pathidentity.Options{FollowLeaf: true})
	if err != nil {
		return "", false
	}
	matches := matchingWorkspaceIDs(state, identity)
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}

// Clone preserves unknown fields while isolating every mutable organization
// field. Read-side import planning must never edit a published snapshot.
func (o Organization) Clone() Organization {
	o.Order = slices.Clone(o.Order)
	o.Imported = maps.Clone(o.Imported)
	o.extra = cloneUnknownFields(o.extra)
	o.Groups = slices.Clone(o.Groups)
	for i := range o.Groups {
		o.Groups[i].Members = slices.Clone(o.Groups[i].Members)
		o.Groups[i].extra = cloneUnknownFields(o.Groups[i].extra)
	}
	return o
}

func cloneUnknownFields(fields map[string]json.RawMessage) map[string]json.RawMessage {
	if fields == nil {
		return nil
	}
	clone := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		clone[key] = bytes.Clone(value)
	}
	return clone
}
