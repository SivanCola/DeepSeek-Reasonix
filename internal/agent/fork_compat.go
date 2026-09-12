package agent

import "reasonix/internal/evidence"

// ebmState is retained only to read and reproduce historical experiment fork
// bundles. The EBM and reasoning-governor enforcement paths are retired.
type ebmState struct {
	fired        bool
	captureArmed bool
	captured     bool
	captureRound int
}

const (
	ebmEnabled      = false
	governorEnabled = false
)

func governorTrigger(sample evidence.OutcomeSample, lastReasoning int) bool {
	return sample.DebtAge == 0 && !sample.LocalExecSeen && lastReasoning >= govReasoningThreshold
}
