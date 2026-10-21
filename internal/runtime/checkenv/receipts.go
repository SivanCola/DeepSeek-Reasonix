package checkenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sync"

	"reasonix/internal/safety/evidence"
)

// A restart or eviction makes old observations unknown and requires a recheck.
// Persisted JSON, including a caller's Complete=true, cannot mint this authority.
var receipts = struct {
	sync.Mutex
	inputs map[string]ownedReceipt
	order  []string
}{inputs: map[string]ownedReceipt{}}

type ownedReceipt struct {
	input     evidence.CheckInputs
	execution string
}

func Verify(input evidence.CheckInputs, executionID string) bool {
	if input.EnvironmentReceipt == "" {
		return false
	}
	receipts.Lock()
	defer receipts.Unlock()
	owned, exists := receipts.inputs[input.EnvironmentReceipt]
	return exists && owned.execution == executionID && reflect.DeepEqual(owned.input, input)
}

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
