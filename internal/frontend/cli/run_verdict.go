package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"reasonix/internal/contract/event"
	"reasonix/internal/contract/eventwire"
	"reasonix/internal/runtime/agent"
)

// runExitUnverified is `run --fail-on-unverified`'s exit status when the model
// finished but the host's final-readiness judgement was left unmet. It is
// apart from 1 (run error) and 2 (usage) so automation can tell them apart.
const runExitUnverified = 3

// runVerdict is what the host decided about the run beside its answer.
type runVerdict struct {
	completion *eventwire.CompletionSummary
}

func (v *runVerdict) observe(e event.Event) {
	if e.Kind == event.CompletionSummary && e.Completion != nil {
		v.completion = eventwire.ToWire(e).Completion
	}
}

// writeDenialWarning tells a text run's diagnostic stream which calls a
// permission gate refused; its stdout carries only the answer.
func writeDenialWarning(w io.Writer, denials []runPermissionDenial) {
	if w == nil || len(denials) == 0 {
		return
	}
	names := make([]string, 0, len(denials))
	for _, d := range denials {
		names = append(names, d.ToolName)
	}
	fmt.Fprintf(w, "warning: the permission policy refused %d tool call(s): %s\n", len(denials), strings.Join(names, ", "))
}

// runReadiness is the unmet final-readiness judgement a run ended with, or nil.
func runReadiness(runErr error) *eventwire.FinalReadiness {
	var readiness *agent.FinalReadinessError
	if !errors.As(runErr, &readiness) || readiness == nil {
		return nil
	}
	return &eventwire.FinalReadiness{Attempts: readiness.Attempts, Missing: append([]string(nil), readiness.Missing...)}
}

// withFailOnUnverified applies --fail-on-unverified to the exit status only;
// the machine-readable result reports the same verdict either way.
func (c runCompletion) withFailOnUnverified(on bool) runCompletion {
	if on && c.unverified {
		c.exitCode = runExitUnverified
	}
	return c
}
