package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/nilutil"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// PolicyPrompt is the fixed recovery-reviewer system prompt. It is stable so
// the reviewer session can keep a cacheable prefix across reviews.
const PolicyPrompt = `You are an independent recovery reviewer for a coding agent.
You do not execute tools and you do not write code. Your only job is to decide
whether the agent's next proposed mutation after a failure is the same safe
strategy, or whether a human must confirm because strategy, scope, or risk changed.

Reply with a single JSON object and nothing else:
{
  "outcome": "continue" | "confirm",
  "change_kind": "same_strategy" | "strategy" | "scope" | "risk" | "uncertain",
  "failure_summary": "short failure summary",
  "diagnosis": "what the diagnosis suggests",
  "proposed_action": "what the agent wants to do next",
  "rationale": "why continue or confirm"
}

Rules:
- outcome=continue ONLY with change_kind=same_strategy when the next action is
  clearly the same method and scope, without higher risk.
- Prefer confirm when unsure.
- Changing tools, expanding write paths, deleting, installing dependencies,
  editing config, or external/network writes must be confirm with the matching
  change_kind.
- Do not invent facts beyond the provided failure, diagnosis, and proposal.`

// Session is a long-lived recovery reviewer with its own agent session,
// isolated from Guardian and the main agent. It has no write tools.
type Session struct {
	prov  provider.Provider
	agent *agent.Agent
	sess  *agent.Session

	mu      sync.Mutex
	timeout time.Duration
}

// NewSession creates a recovery reviewer. temperature should be 0 for
// deterministic JSON. sink is discarded for reviewer chatter.
func NewSession(prov provider.Provider, modelRef string, temperature float64, pricing *provider.Pricing) *Session {
	_ = modelRef // caller selects provider; modelRef kept for API symmetry with guardian
	reg := tool.NewRegistry() // empty: no tools
	sess := agent.NewSession(PolicyPrompt)
	ag := agent.New(prov, reg, sess, agent.Options{
		MaxSteps:            1,
		Temperature:         temperature,
		ContextWindow:       32_000,
		CompactRatio:        0.9,
		SoftCompactRatio:    0.7,
		ToolResultSnipRatio: 0.6,
		CompactForceRatio:   0.95,
	}, event.Discard)
	if pricing != nil {
		// pricing is optional telemetry; agent.Options already accepted via New
		_ = pricing
	}
	return &Session{
		prov:    prov,
		agent:   ag,
		sess:    sess,
		timeout: 30 * time.Second,
	}
}

// Review implements Reviewer.
func (s *Session) Review(ctx context.Context, failure *FailureEvent, diagnosis []string, proposal Proposal, taskSummary string) (ReviewVerdict, error) {
	if s == nil || s.agent == nil {
		return ReviewVerdict{}, fmt.Errorf("recovery reviewer unavailable")
	}
	if nilutil.IsNil(ctx) {
		ctx = context.Background()
	}
	reviewCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	prompt := buildReviewPrompt(failure, diagnosis, proposal, taskSummary)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Keep a short rolling transcript: system prompt stays fixed; each review
	// is a fresh user message. Reset session messages except system to bound
	// growth while preserving the stable system prefix.
	s.sess.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: PolicyPrompt},
	})

	err := s.agent.Run(reviewCtx, prompt)
	if err != nil {
		return ReviewVerdict{}, err
	}
	text := lastAssistantText(s.sess.Snapshot())
	verdict, perr := parseReviewVerdict(text)
	if perr != nil {
		return ReviewVerdict{}, perr
	}
	return verdict, nil
}

// Close releases reviewer resources.
func (s *Session) Close() {
	// Agent has no explicit Close; session is GC'd with the controller.
}

func buildReviewPrompt(failure *FailureEvent, diagnosis []string, proposal Proposal, taskSummary string) string {
	var b strings.Builder
	b.WriteString("Review this recovery proposal.\n\n")
	if strings.TrimSpace(taskSummary) != "" {
		b.WriteString("Task summary:\n")
		b.WriteString(strings.TrimSpace(taskSummary))
		b.WriteString("\n\n")
	}
	if failure != nil {
		b.WriteString("Failure:\n")
		b.WriteString(fmt.Sprintf("- tool: %s\n", failure.Tool))
		if failure.Subject != "" {
			b.WriteString(fmt.Sprintf("- subject: %s\n", failure.Subject))
		}
		if failure.ErrSummary != "" {
			b.WriteString(fmt.Sprintf("- error: %s\n", failure.ErrSummary))
		}
		if failure.ArgsSummary != "" {
			b.WriteString(fmt.Sprintf("- args: %s\n", failure.ArgsSummary))
		}
		b.WriteString(fmt.Sprintf("- verification: %v\n", failure.Verification))
		b.WriteString(fmt.Sprintf("- mutates: %v\n", failure.Mutates))
		b.WriteString(fmt.Sprintf("- repeat_count: %d\n", failure.RepeatCount))
		if failure.OutputExcerpt != "" {
			b.WriteString("- output excerpt:\n")
			b.WriteString(failure.OutputExcerpt)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(diagnosis) > 0 {
		b.WriteString("Diagnosis notes:\n")
		for _, d := range diagnosis {
			b.WriteString("- ")
			b.WriteString(d)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Proposed next mutation:\n")
	b.WriteString(fmt.Sprintf("- tool: %s\n", proposal.Tool))
	if proposal.Subject != "" {
		b.WriteString(fmt.Sprintf("- subject: %s\n", proposal.Subject))
	}
	if proposal.Preview != "" {
		b.WriteString(fmt.Sprintf("- preview: %s\n", proposal.Preview))
	}
	if len(proposal.Args) > 0 {
		b.WriteString(fmt.Sprintf("- args: %s\n", ArgsSummary(proposal.Args, 400)))
	}
	b.WriteString(fmt.Sprintf("- mutates: %v\n", proposal.Mutates))
	b.WriteString(fmt.Sprintf("- verification: %v\n", proposal.Verification))
	b.WriteString(fmt.Sprintf("- high_risk: %v\n", proposal.HighRisk))
	b.WriteString(fmt.Sprintf("- expanded_scope: %v\n", proposal.ExpandedScope))
	b.WriteString(fmt.Sprintf("- strategy_changed: %v\n", proposal.StrategyChanged))
	b.WriteString("\nRespond with JSON only.")
	return b.String()
}

func lastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleAssistant {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}

func parseReviewVerdict(text string) (ReviewVerdict, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return ReviewVerdict{}, fmt.Errorf("empty recovery reviewer response")
	}
	// Extract JSON object if the model wrapped it in fences.
	if i := strings.Index(text, "{"); i >= 0 {
		if j := strings.LastIndex(text, "}"); j > i {
			text = text[i : j+1]
		}
	}
	var v ReviewVerdict
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return ReviewVerdict{}, fmt.Errorf("invalid recovery reviewer JSON: %w", err)
	}
	if strings.TrimSpace(string(v.Outcome)) == "" {
		return ReviewVerdict{}, fmt.Errorf("recovery reviewer JSON missing outcome")
	}
	return v, nil
}
