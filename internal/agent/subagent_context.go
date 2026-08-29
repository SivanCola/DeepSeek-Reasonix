package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"reasonix/internal/evidence"
)

const defaultReviewMaxSteps = 8
const defaultReviewOutputTokens = 2048

func composeChildTaskPrompt(spec ProfileExecSpec) string {
	var b strings.Builder
	b.WriteString("## Task\n")
	b.WriteString(strings.TrimSpace(spec.Task.Objective))
	if ctx := spec.Context; ctxHasFacts(ctx) {
		if len(ctx.Decisions) > 0 {
			b.WriteString("\n\n## Confirmed decisions\n")
			for _, dec := range ctx.Decisions {
				fmt.Fprintf(&b, "- %s (%s): %s\n", dec.Question, dec.ID, dec.Answer)
			}
		}
		if strings.TrimSpace(ctx.EvidenceSummary) != "" {
			b.WriteString("\n## Evidence summary\n")
			b.WriteString(strings.TrimSpace(ctx.EvidenceSummary))
			b.WriteByte('\n')
		}
		if len(ctx.FileAnchors) > 0 {
			b.WriteString("\n## File anchors\n")
			for _, path := range ctx.FileAnchors {
				fmt.Fprintf(&b, "- %s\n", path)
			}
		}
		if strings.TrimSpace(ctx.OutputFormat) != "" {
			b.WriteString("\n## Output format\n")
			b.WriteString(strings.TrimSpace(ctx.OutputFormat))
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nDo not copy or reconstruct the parent session. Use only this pack plus tools.")
	return strings.TrimSpace(b.String())
}

func ctxHasFacts(ctx ContextRequest) bool {
	return len(ctx.Decisions) > 0 || strings.TrimSpace(ctx.EvidenceSummary) != "" ||
		len(ctx.FileAnchors) > 0 || strings.TrimSpace(ctx.OutputFormat) != ""
}

func applyReviewBudget(spec *ProfileExecSpec) {
	if spec == nil {
		return
	}
	switch strings.TrimSpace(spec.Worker.Profile) {
	case "review", "security-review", "security_review", "team-architect":
		if spec.Sched.MaxSteps <= 0 {
			spec.Sched.MaxSteps = defaultReviewMaxSteps
		}
		if spec.Sched.MaxOutputTokens <= 0 {
			spec.Sched.MaxOutputTokens = defaultReviewOutputTokens
		}
		if strings.TrimSpace(spec.Context.OutputFormat) == "" {
			spec.Context.OutputFormat = "Return structured fields only: verdict, blocking_findings, non_blocking, required_changes. Do not restate full files or full test logs."
		}
	}
}

type childOutputBudgetKey struct{}

func withChildOutputBudget(ctx context.Context, n int) context.Context {
	if n <= 0 {
		return ctx
	}
	return context.WithValue(ctx, childOutputBudgetKey{}, n)
}

func childOutputBudgetFrom(ctx context.Context) int {
	n, _ := ctx.Value(childOutputBudgetKey{}).(int)
	return n
}

func fillChildFacts(ctx context.Context, spec *ProfileExecSpec) {
	if spec == nil {
		return
	}
	if len(spec.Context.Decisions) == 0 {
		if turn := turnStateFrom(ctx); turn != nil {
			spec.Context.Decisions = turn.loop.snapshotDecisions()
		}
	}
	ledger, ok := evidence.FromContext(ctx)
	if !ok {
		return
	}
	summary, anchors := compactParentFacts(ledger)
	if spec.Context.EvidenceSummary == "" {
		spec.Context.EvidenceSummary = summary
	}
	if len(spec.Context.FileAnchors) == 0 {
		spec.Context.FileAnchors = anchors
	}
}

func compactParentFacts(ledger *evidence.Ledger) (string, []string) {
	if ledger == nil {
		return "", nil
	}
	receipts := ledger.Receipts()
	successes, mutations, reads := 0, 0, 0
	seen := map[string]bool{}
	var anchors []string
	for _, rec := range receipts {
		if !rec.Success {
			continue
		}
		successes++
		if rec.Mutation || rec.Write {
			mutations++
		}
		if rec.Read {
			reads++
		}
		for _, path := range rec.Paths {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			if len(anchors) < 16 {
				anchors = append(anchors, path)
			}
		}
	}
	if successes == 0 {
		return "", anchors
	}
	return fmt.Sprintf("%d successful receipts (%d mutations, %d reads).", successes, mutations, reads), anchors
}

func (s *turnLoopState) snapshotDecisions() []acceptedDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.acceptedDecisions) == 0 {
		return nil
	}
	out := make([]acceptedDecision, 0, len(s.acceptedDecisions))
	for _, dec := range s.acceptedDecisions {
		out = append(out, dec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
