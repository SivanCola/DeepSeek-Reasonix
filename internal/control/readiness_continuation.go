package control

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/evidence"
	"reasonix/internal/i18n"
)

// A turn that ends owing verification has not failed: the host read what is
// missing from its own receipts, so asking the user to press continue only asks
// them to relay a message the host already knows. Bound automatic continuation
// by whether the gap is still shrinking instead.
const readinessStallRounds = 2

// continueUntilReady runs known missing requirements as synthetic follow-up
// turns. The returned error is the last turn's outcome, so callers see one
// foreground operation regardless of how many bounded continuations it used.
func (o *turnOrchestrator) continueUntilReady(ctx context.Context, turnErr error) error {
	best, stall := -1, 0
	for {
		var readinessErr *agent.FinalReadinessError
		if !errors.As(turnErr, &readinessErr) {
			return turnErr
		}
		gap := len(readinessErr.Missing)
		switch {
		case best < 0 || gap < best:
			best, stall = gap, 0
		default:
			// Compare against the best round, not only the previous round, so
			// an oscillating gap cannot reset the bound and run forever.
			stall++
			if stall >= readinessStallRounds {
				return turnErr
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		prompt := readinessContinuationPrompt(o.c.goalTodos(), readinessErr.Reason)
		if prompt == "" {
			return turnErr
		}
		// Preserve the finished turn's receipts. Starting with an empty ledger
		// would make the gap disappear because its evidence was dropped, not
		// because the remaining checks actually passed.
		if o.c.executor == nil || !o.c.executor.PrepareFinalReadinessRecovery() {
			return turnErr
		}
		o.c.noticeDetail(i18n.M.ReadinessContinuing, prompt)
		turnErr = o.runOrchestratedTurn(ctx, orchestratedTurn{
			input: prompt, raw: prompt, synthetic: true,
		})
	}
}

// readinessContinuationPrompt states only host-observed missing work. It is an
// append-only user turn, leaving the system prompt and tool-schema cache prefix
// unchanged.
func readinessContinuationPrompt(todos []evidence.TodoItem, reason string) string {
	var parts []string
	if incomplete := evidence.IncompleteTodos(todos); len(incomplete) > 0 {
		var b strings.Builder
		b.WriteString("these tasks are still incomplete:")
		for _, todo := range incomplete {
			fmt.Fprintf(&b, "\n  - %s (%s)", todo.Content, todo.Status)
		}
		parts = append(parts, b.String())
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		parts = append(parts, reason)
	}
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(agent.ReadinessContinuationPrefix + "\n")
	for _, part := range parts {
		b.WriteString("- " + part + "\n")
	}
	b.WriteString("Finish it now. Do the remaining work, then verify it and record the outcome.")
	return b.String()
}
