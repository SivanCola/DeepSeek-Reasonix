package control

import "strings"

const (
	// FinalReadinessRecoveryAction is the typed transport action used by HTTP
	// and ACP. It is additive and optional, so older clients remain compatible.
	FinalReadinessRecoveryAction = "final_readiness_recovery"
	ContinueChecksCommand        = "/continue-checks"
	defaultContinueChecksPrompt  = "Continue the remaining final checks, preserve completed work, and only finish after the host readiness requirements pass."
)

// ParseFinalReadinessRecoveryCommand converts the explicit slash action into a
// model prompt. Optional trailing text is user guidance; the command token
// itself never reaches the provider.
func ParseFinalReadinessRecoveryCommand(input string) (prompt string, ok bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed != ContinueChecksCommand && !strings.HasPrefix(trimmed, ContinueChecksCommand+" ") {
		return "", false
	}
	prompt = strings.TrimSpace(strings.TrimPrefix(trimmed, ContinueChecksCommand))
	if prompt == "" {
		prompt = defaultContinueChecksPrompt
	}
	return prompt, true
}
