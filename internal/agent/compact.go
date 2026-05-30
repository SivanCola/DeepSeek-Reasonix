package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Compaction defaults. Compaction is a low-frequency cache-reset point: prompts
// grow prepend-only (high cache hits) until a turn's prompt nears the model's
// context window, then we compact once — summarizing the older history and
// archiving the originals — so a long task can keep going.
const (
	defaultCompactRatio = 0.8 // compact when prompt_tokens reach this fraction of the window
	defaultRecentKeep   = 8   // recent messages kept verbatim, never summarized
	minCompactMessages  = 2   // skip compaction below this many compactable messages
)

// Fold economics: normal-band compaction should only run when the expected
// multi-turn savings justify the immediate cost of a cold summary segment.
const (
	foldEconHorizonTurns       = 3
	foldEconMinSavingsFraction = 0.15
	foldEconMinSavingsUSD      = 0.002
	foldSummaryReserveTokens   = 4096
)

// summarySystemPrompt steers the executor to distill older history into a
// structured briefing it can keep relying on after the originals are dropped.
const summarySystemPrompt = `You are compacting the earlier part of a coding agent's conversation to save context.
Summarize the messages below into a compact briefing the agent can rely on to continue the task.
Structure the summary with these sections, each as a terse bullet list:

## Goal
The user's original task and any explicit requirements or constraints.

## Decisions
Key decisions made and their rationale (why, not just what).

## Files
Files read or modified, with the important facts learned about each.

## Commands
Commands run and their relevant outcomes (especially errors or unexpected results).

## Pending
What is still in progress or not yet done.

Omit small talk and redundant detail. Do not invent information.`

// summaryTag marks a compacted summary message so tools and diagnostics can
// distinguish it from an ordinary user message.
const summaryTag = "[compacted]"

// foldEconomics estimates whether compacting is cheaper than carrying the
// current input cost. It models the cost of a fold: one summary call
// (input-cold for the full prompt), plus the post-fold cold miss for the
// next real turn, plus (horizon-1) warm turns on the smaller prefix —
// and compares it against carrying the current cost for horizon turns.
func foldEconomics(u *provider.Usage, pricing *provider.Pricing, ctxWindow int) (savings float64, worthwhile bool) {
	if pricing == nil || u == nil || u.PromptTokens == 0 {
		return 0, true // no pricing data: let the fold proceed
	}
	horizonTurns := foldEconHorizonTurns
	// Carrying current input cost for horizon turns (all warm after first).
	carryCost := (float64(u.CacheMissTokens)*pricing.Input +
		float64(u.CacheHitTokens)*pricing.CacheHit) / 1e6 * float64(horizonTurns)

	// Fold: one summary call (all-input-cold) + post-fold cold + (horizon-1) warm.
	summaryCost := float64(u.PromptTokens) * pricing.Input / 1e6
	postFoldTokens := float64(u.PromptTokens)
	tailBudget := float64(ctxWindow) * 0.3 // HISTORY_FOLD_TAIL_FRACTION = 0.3
	if postFoldTokens > tailBudget+foldSummaryReserveTokens {
		postFoldTokens = tailBudget + foldSummaryReserveTokens
	}
	postFoldCold := postFoldTokens * pricing.Input / 1e6
	postFoldWarm := postFoldTokens * pricing.CacheHit / 1e6 * float64(horizonTurns-1)
	foldCost := summaryCost + postFoldCold + postFoldWarm

	savings = carryCost - foldCost
	fraction := 0.0
	if carryCost > 0 {
		fraction = savings / carryCost
	}
	worthwhile = savings >= foldEconMinSavingsUSD && fraction >= foldEconMinSavingsFraction
	return savings, worthwhile
}

// maybeCompact compacts the session when the last turn's prompt has grown to the
// configured fraction of the context window. It is a no-op when compaction is
// disabled (no window) or usage is unavailable.
func (a *Agent) maybeCompact(ctx context.Context, u *provider.Usage) {
	if a.contextWindow <= 0 || u == nil || u.PromptTokens == 0 {
		return
	}
	ratio := float64(u.PromptTokens) / float64(a.contextWindow)
	// Aggressive threshold: always compact to protect the context window.
	if ratio >= 0.9 {
		_ = a.compact(ctx)
		return
	}
	// Normal band: only compact if economics say it's worth it.
	if ratio < a.compactRatio {
		return
	}
	_, worthwhile := foldEconomics(u, a.pricing, a.contextWindow)
	if !worthwhile {
		return
	}
	if err := a.compact(ctx); err != nil {
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf("compaction skipped: %v", err)})
	}
}

// compact summarizes the older middle of the session and replaces it in place:
// the session becomes system + [summary] + recent tail. The dropped originals
// are archived first, so the full history stays traceable. The summary message
// is tagged with summaryTag so diagnostics and tools can distinguish it.
func (a *Agent) compact(ctx context.Context) (err error) {
	emit := func(phase string, progress float64) {
		a.sink.Emit(event.Event{Kind: event.CompactProgress, Text: phase, Progress: progress})
	}

	emit("start", 0.0)
	defer func() {
		if err != nil {
			emit("done", 1.0)
		}
	}()

	msgs := a.session.Messages
	head, start, ok := compactBounds(msgs, a.recentKeep, minCompactMessages, a.keepPolicy)
	if !ok {
		emit("done", 1.0)
		return nil // recent tail already covers everything worth keeping
	}
	region := msgs[head:start]

	emit("archive", 0.1)

	archived := ""
	if a.archiveDir != "" {
		path, e := archiveMessages(a.archiveDir, region)
		if e != nil {
			return fmt.Errorf("archive: %w", e)
		}
		archived = path
	}

	emit("summarize", 0.2)

	summary, e := a.summarize(ctx, region)
	if e != nil {
		return e
	}

	emit("rebuild", 0.85)

	compacted := make([]provider.Message, 0, head+1+len(msgs)-start)
	compacted = append(compacted, msgs[:head]...)
	compacted = append(compacted, provider.Message{
		Role:    provider.RoleUser,
		Content: summaryTag + "\n" + summary,
	})
	compacted = append(compacted, msgs[start:]...)
	a.session.Messages = compacted
	a.session.IncrementRewrite()

	emit("done", 1.0)

	note := fmt.Sprintf("compacted %d messages → summary", len(region))
	if archived != "" {
		note += " (archived " + archived + ")"
	}
	a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: note})
	return nil
}

// compactBounds locates the region to summarize. head is the count of leading
// messages preserved verbatim (the system prompt, if any); start is where the
// preserved recent tail begins, so msgs[head:start] is compacted. The boundary
// is aligned backward off any tool result so the recent tail never begins with
// an orphan tool message whose assistant tool_calls were summarized away.
//
// keepPolicy extends the recent tail: KeepErrors moves tool results with
// non-zero exit status into the tail; KeepUserMarked moves user messages
// whose content starts with "[keep]" into the tail. The tail is still bounded
// by head+minCompact to avoid degenerate compactions.
//
// ok is false when there is too little to compact.
func compactBounds(msgs []provider.Message, recentKeep, minCompact int, keepPolicy KeepPolicy) (head, start int, ok bool) {
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		head = 1
	}
	start = len(msgs) - recentKeep
	if start <= head {
		return head, start, false
	}
	for start > head && msgs[start].Role == provider.RoleTool {
		start--
	}
	// KeepPolicy: extend tail to preserve error results and marked messages.
	extra := 0
	for i := head; i < start; i++ {
		m := msgs[i]
		if keepPolicy&KeepErrors != 0 && m.Role == provider.RoleTool && isErrorResult(m.Content) {
			extra++
		}
		if keepPolicy&KeepUserMarked != 0 && m.Role == provider.RoleUser && strings.HasPrefix(strings.ToLower(strings.TrimSpace(m.Content)), "[keep]") {
			extra++
		}
	}
	start -= extra
	if start <= head {
		start = head + 1
	}
	if start-head < minCompact {
		return head, start, false
	}
	return head, start, true
}

// isErrorResult returns true when tool output looks like an execution failure.
func isErrorResult(content string) bool {
	s := strings.TrimSpace(content)
	return strings.HasPrefix(s, "error:") ||
		strings.HasPrefix(s, "exit status") ||
		strings.Contains(s, "\nexit status")
}

// summarize asks the executor's own provider (no tools) to distill the region
// into a briefing, returning the collected text.
func (a *Agent) summarize(ctx context.Context, region []provider.Message) (string, error) {
	ch, err := a.prov.Stream(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: summarySystemPrompt},
			{Role: provider.RoleUser, Content: renderTranscript(region)},
		},
		Temperature: a.temperature,
	})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkText:
			b.WriteString(chunk.Text)
		case provider.ChunkError:
			return "", chunk.Err
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "", fmt.Errorf("summarizer returned empty output")
	}
	return s, nil
}

// renderTranscript flattens messages into a readable transcript for summarization.
func renderTranscript(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			fmt.Fprintf(&b, "[user]\n%s\n\n", m.Content)
		case provider.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "[assistant]\n%s\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[assistant calls %s] %s\n", tc.Name, tc.Arguments)
			}
			b.WriteString("\n")
		case provider.RoleTool:
			fmt.Fprintf(&b, "[tool %s result]\n%s\n\n", m.Name, m.Content)
		case provider.RoleSystem:
			fmt.Fprintf(&b, "[system]\n%s\n\n", m.Content)
		}
	}
	return b.String()
}

// archiveMessages writes the dropped originals to a timestamped .jsonl (one
// message per line) under dir, returning the file path.
func archiveMessages(dir string, msgs []provider.Message) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405.000")+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			return "", err
		}
	}
	return path, nil
}
