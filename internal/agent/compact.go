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

// summarySystemPrompt steers the executor to distill older history into a
// briefing it can keep relying on after the originals are dropped.
const summarySystemPrompt = `You are compacting the earlier part of a coding agent's conversation to save context.
Summarize the messages below into a compact briefing the agent can rely on to continue the task. Preserve:
- the user's goal and any explicit requirements or constraints
- key decisions made and their rationale
- files read or modified and the important facts learned about them
- commands run and their relevant outcomes
- what is still pending or in progress
Omit small talk and redundant detail. Use terse bullet points. Do not invent information.`

// maybeCompact compacts the session when the last turn's prompt has grown to the
// configured fraction of the context window. It is a no-op when compaction is
// disabled (no window) or usage is unavailable.
func (a *Agent) maybeCompact(ctx context.Context, u *provider.Usage) {
	if a.contextWindow <= 0 || u == nil || u.PromptTokens == 0 {
		return
	}
	if u.PromptTokens < int(float64(a.contextWindow)*a.compactRatio) {
		return
	}
	if err := a.compact(ctx); err != nil {
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf("compaction skipped: %v", err)})
	}
}

// compact summarizes the older middle of the session and replaces it in place:
// the session becomes system + summary + recent tail. The dropped originals are
// archived first, so the full history stays traceable.
func (a *Agent) compact(ctx context.Context) error {
	msgs := a.session.Messages
	head, start, ok := compactBounds(msgs, a.recentKeep, minCompactMessages)
	if !ok {
		return nil // recent tail already covers everything worth keeping
	}
	region := msgs[head:start]
	foldRegion, kept := splitKeepPolicy(region, a.keepPolicy)
	if len(foldRegion) == 0 {
		return nil
	}

	archived := ""
	if a.archiveDir != "" {
		path, err := archiveMessages(a.archiveDir, foldRegion)
		if err != nil {
			return fmt.Errorf("archive: %w", err)
		}
		archived = path
	}

	summary, err := a.summarize(ctx, foldRegion)
	if err != nil {
		return err
	}

	compacted := make([]provider.Message, 0, head+1+len(kept)+len(msgs)-start)
	compacted = append(compacted, msgs[:head]...)
	compacted = append(compacted, provider.Message{
		Role:    provider.RoleUser,
		Content: "Summary of earlier conversation (older messages were compacted to save context):\n" + summary,
	})
	compacted = append(compacted, kept...)
	compacted = append(compacted, msgs[start:]...)
	a.session.Messages = compacted

	note := fmt.Sprintf("compacted %d messages → summary", len(foldRegion))
	if archived != "" {
		note += " (archived " + archived + ")"
	}
	a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: note})
	return nil
}

func splitKeepPolicy(region []provider.Message, policy KeepPolicy) (fold []provider.Message, kept []provider.Message) {
	keep := keepIndexes(region, policy)
	for i, m := range region {
		if keep[i] {
			kept = append(kept, m)
		} else {
			fold = append(fold, m)
		}
	}
	return fold, kept
}

func keepIndexes(region []provider.Message, policy KeepPolicy) []bool {
	keep := make([]bool, len(region))
	for i, m := range region {
		if shouldKeepMessage(m, policy) {
			keep[i] = true
		}
	}
	for i, m := range region {
		if !keep[i] {
			continue
		}
		switch m.Role {
		case provider.RoleTool:
			if j := findToolCaller(region, i, m.ToolCallID); j >= 0 {
				keep[j] = true
			}
		case provider.RoleAssistant:
			ids := toolCallIDs(m)
			for j := i + 1; j < len(region) && region[j].Role == provider.RoleTool; j++ {
				if ids[region[j].ToolCallID] {
					keep[j] = true
				}
			}
		}
	}
	return keep
}

func shouldKeepMessage(m provider.Message, policy KeepPolicy) bool {
	if policy&KeepErrors != 0 && isErrorMessage(m) {
		return true
	}
	if policy&KeepUserMarked != 0 && isUserMarked(m) {
		return true
	}
	return false
}

func isErrorMessage(m provider.Message) bool {
	if m.Role != provider.RoleTool {
		return false
	}
	s := strings.TrimSpace(strings.ToLower(m.Content))
	return strings.HasPrefix(s, "error:") || strings.HasPrefix(s, "blocked:")
}

func isUserMarked(m provider.Message) bool {
	content := strings.ToLower(m.Content)
	return strings.Contains(content, "[[keep]]") ||
		strings.Contains(content, "[keep]") ||
		strings.Contains(content, "<keep>") ||
		strings.Contains(content, "<!-- keep -->")
}

func findToolCaller(region []provider.Message, toolIndex int, id string) int {
	for i := toolIndex - 1; i >= 0; i-- {
		if region[i].Role != provider.RoleAssistant {
			continue
		}
		for _, tc := range region[i].ToolCalls {
			if tc.ID == id {
				return i
			}
		}
	}
	return -1
}

func toolCallIDs(m provider.Message) map[string]bool {
	ids := make(map[string]bool, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		ids[tc.ID] = true
	}
	return ids
}

// SummarizeFrom replaces the messages from fromIdx onward with a single summary,
// keeping everything before it verbatim ("summarize from here"). fromIdx is a turn
// boundary (a user message), so the split never severs a tool_call/result pair —
// those live within one turn. A no-op when the region is empty.
func (a *Agent) SummarizeFrom(ctx context.Context, fromIdx int) error {
	msgs := a.session.Messages
	if fromIdx < 0 || fromIdx >= len(msgs) {
		return nil
	}
	region := msgs[fromIdx:]
	if a.archiveDir != "" {
		_, _ = archiveMessages(a.archiveDir, region) // best-effort traceability
	}
	summary, err := a.summarize(ctx, region)
	if err != nil {
		return err
	}
	next := make([]provider.Message, 0, fromIdx+1)
	next = append(next, msgs[:fromIdx]...)
	next = append(next, provider.Message{
		Role:    provider.RoleUser,
		Content: "Summary of the later conversation (compacted from here on):\n" + summary,
	})
	a.session.Messages = next
	a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("summarized %d later messages → summary", len(region))})
	return nil
}

// SummarizeUpTo replaces the messages before toIdx (after the system prompt) with
// a single summary, keeping toIdx onward verbatim ("summarize up to here"). toIdx
// is a turn boundary, so no tool pair is split. A no-op when the region is empty.
func (a *Agent) SummarizeUpTo(ctx context.Context, toIdx int) error {
	msgs := a.session.Messages
	head := 0
	if len(msgs) > 0 && msgs[0].Role == provider.RoleSystem {
		head = 1
	}
	if toIdx <= head || toIdx > len(msgs) {
		return nil
	}
	region := msgs[head:toIdx]
	if a.archiveDir != "" {
		_, _ = archiveMessages(a.archiveDir, region)
	}
	summary, err := a.summarize(ctx, region)
	if err != nil {
		return err
	}
	next := make([]provider.Message, 0, head+1+len(msgs)-toIdx)
	next = append(next, msgs[:head]...)
	next = append(next, provider.Message{
		Role:    provider.RoleUser,
		Content: "Summary of earlier conversation (compacted up to here):\n" + summary,
	})
	next = append(next, msgs[toIdx:]...)
	a.session.Messages = next
	a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("summarized %d earlier messages → summary", len(region))})
	return nil
}

// compactBounds locates the region to summarize. head is the count of leading
// messages preserved verbatim (the system prompt, if any); start is where the
// preserved recent tail begins, so msgs[head:start] is compacted. The boundary
// is aligned backward off any tool result so the recent tail never begins with
// an orphan tool message whose assistant tool_calls were summarized away. ok is
// false when there is too little to compact.
func compactBounds(msgs []provider.Message, recentKeep, minCompact int) (head, start int, ok bool) {
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
	if start-head < minCompact {
		return head, start, false
	}
	return head, start, true
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
