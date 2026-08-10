package provider

import (
	"time"
)

// CompactionCapabilities describes model/provider cache and output limits used
// by request budgeting. Native provider compaction was removed; all providers
// use Reasonix local summary checkpoints.
type CompactionCapabilities struct {
	NativeCompaction       bool // always false for new code paths
	MaxOutputTokens        int
	CompactionOutputTokens int
	CacheTTL               time.Duration
}

// CompactionCapabler is an optional Provider surface for capability lookup.
type CompactionCapabler interface {
	CompactionCapabilities() CompactionCapabilities
}

// AsCompactionCapabler returns p when it implements CompactionCapabler.
func AsCompactionCapabler(p Provider) (CompactionCapabler, bool) {
	if p == nil {
		return nil, false
	}
	cc, ok := p.(CompactionCapabler)
	return cc, ok
}
