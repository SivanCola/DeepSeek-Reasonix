package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Compaction is a low-frequency cache-reset point: the prompt grows append-only
// (high cache hits) until a turn nears compactRatio of the window, then it is
// compacted down to a tail budget. The budget is a fixed token count, not a
// fraction of the window, so a huge window still compacts rarely while a small
// one still lands below the trigger (which is what stops the re-compaction loop).
const (
	defaultSoftCompactRatio    = 0.5   // report growing context here, but keep the cache-stable prefix intact
	defaultToolResultSnipRatio = 0.6   // rewrite stale tool results cheaply before summary compaction
	defaultCompactRatio        = 0.8   // trigger: prompt at this fraction of the window compacts
	defaultCompactForceRatio   = 0.9   // force compaction at this high-water mark even for low-value folds
	defaultCompactTarget       = 0.5   // safety cap: the kept tail never exceeds this fraction of the window
	defaultTailTokens          = 16384 // verbatim recent-tail budget, in tokens
	minRecentKeep              = 2     // never keep fewer recent messages than this
	minCompactMessages         = 2     // skip compaction below this many compactable messages
	fallbackTokPerChar         = 0.25  // ~4 chars/token, used before any usage is available to calibrate
	maxPinnedFirstUserTokens   = 1500  // ceiling on pinning the first user turn verbatim; larger first turns (pasted content) stay foldable
	pinnedFirstUserWindowFrac  = 0.15  // and never pin a first turn worth more than this fraction of the window
)

// summaryTag wraps the compaction summary so the model can distinguish it from
// live user input and later strip or skip it when reasoning about the current turn.
const (
	summaryTagOpen  = "<compaction-summary>"
	summaryTagClose = "</compaction-summary>"
)

// summaryTimeout bounds one summarizer call so a stalled stream surfaces a clear
// failure (then a mechanical fold) instead of hanging compaction indefinitely.
const summaryTimeout = 90 * time.Second

// summarySystemPrompt steers the executor to distill older history into a
// structured briefing it can keep relying on after the originals are dropped.
// Quality gate requires the four primary headings below; detail subsections may
// appear under ## Current state.
const summarySystemPrompt = `You are compacting the earlier part of a coding agent's conversation to save context.
The agent keeps your summary alongside the user's own turns (kept verbatim) and the recent tail; your job is to fold the assistant/tool work into a briefing it can resume from.
Write under these exact headings (all four are required):

## Goal
The user's request and intent, plus standing facts & constraints they stated that still govern the work (names, paths, IDs, versions, hard "never do X" rules) in their own words.

## Completed & decisions
What was finished, key choices made so far and why — so they are not re-litigated or reversed. Include commands run and their outcomes when relevant.

## Current state
Concrete code/workspace state: files read or modified (signatures, locations, data shapes, exact edits), open errors and how they were fixed (or not).

## Pending & next step
What is still in progress or unstarted, and the single most concrete next action to take.

Rules: be terse — bullet points and fragments, not prose. Preserve identifiers, paths, and numbers exactly. Do NOT invent anything not present in the messages; if something is unknown, leave it out rather than guessing.`

// maybeCompact compacts the session when the last turn's prompt has grown to the
// configured fraction of the context window. It is a no-op when compaction is
// disabled (no window) or usage is unavailable.
func (a *Agent) maybeCompact(ctx context.Context, u *provider.Usage) {
	if a.contextWindow <= 0 || u == nil || u.PromptTokens == 0 {
		return
	}
	high := int(float64(a.contextWindow) * a.compactRatio)
	snip := int(float64(a.contextWindow) * a.toolResultSnipRatio)
	soft := int(float64(a.contextWindow) * a.softCompactRatio)
	// Between the soft ratio and the trigger, report growing context once without
	// rewriting the prefix — a compaction here would needlessly crater the cache.
	if u.PromptTokens >= soft && u.PromptTokens < snip && !a.softCompactNoticed {
		a.softCompactNoticed = true
		detail := fmt.Sprintf("context reached %.0f%% of window; keeping cache-first prefix until compact threshold %.0f%%", a.softCompactRatio*100, a.compactRatio*100)
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Context is getting large; preserving cache until cleanup is needed.", Detail: detail})
		return
	}
	if u.PromptTokens >= snip && u.PromptTokens < high {
		ratio := a.tokPerChar()
		if st, err := a.SnipStaleToolResults(); err == nil && st.Results > 0 {
			saved := int(float64(st.SavedChars) * ratio)
			a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(
				"snipped %d stale tool results (~%d tokens est.) before compaction", st.Results, saved)})
		}
		return
	}
	if u.PromptTokens < high {
		// A turn that sits under the trigger is the breathing room a healthy
		// compaction buys; it clears the stuck latch and the run counter.
		a.consecutiveCompacts = 0
		a.compactStuck = false
		return
	}
	if a.compactStuck {
		return
	}
	force := u.PromptTokens >= int(float64(a.contextWindow)*a.compactForceRatio)
	// Prune before folding: when eliding stale tool results alone clears the
	// trigger, this turn's (paid) summarize call is skipped entirely.
	ratio := a.tokPerChar()
	if st, err := a.PruneStaleToolResults(); err == nil && st.Results > 0 {
		saved := int(float64(st.SavedChars) * ratio)
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: fmt.Sprintf(
			"pruned %d stale tool results (~%d tokens est.) before compaction", st.Results, saved)})
		if !force && u.PromptTokens-saved < high {
			return
		}
	}
	if err := a.compact(ctx, "auto", "", force); err != nil {
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Context cleanup skipped for now.", Detail: fmt.Sprintf("compaction skipped: %v", err)})
		return
	}
	// A healthy compaction drops the prompt under the trigger, so the next turn
	// won't compact. Compacting on consecutive turns means the kept tail alone
	// exceeds the trigger — the system prompt plus one verbatim turn is bigger than
	// the window allows. Re-firing every turn is the loop users hit, so pause
	// auto-compaction and say why, once.
	a.consecutiveCompacts++
	if a.consecutiveCompacts >= 2 {
		a.compactStuck = true
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Automatic context cleanup paused because the context window is too small.", Detail: fmt.Sprintf(
			"context_window=%d is too small for compaction to help (the system prompt plus one turn already exceeds %.0f%% of it); raise context_window or shrink tool output. Auto-compaction paused until the prompt drops.",
			a.contextWindow, a.compactRatio*100)})
	}
}

// foldEconomics estimates whether compacting the given region saves enough
// tokens to justify the summarization API call. It returns false when the
// region is too small for the savings to outweigh the extra round-trip cost
// and latency of calling the summarizer.
func foldEconomics(region []provider.Message) bool {
	const minFoldTokens = 400
	return estimateMessagesTokens(region) >= minFoldTokens
}

func estimateMessagesTokens(msgs []provider.Message) int {
	total := 0
	for _, m := range msgs {
		total += 4 // chat-message framing overhead
		total += estimateTextTokens(m.Content)
		total += estimateTextTokens(m.ReasoningContent)
		total += estimateTextTokens(m.Name)
		total += estimateTextTokens(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			total += 8
			total += estimateTextTokens(tc.ID)
			total += estimateTextTokens(tc.Name)
			total += estimateTextTokens(tc.Arguments)
		}
	}
	return total
}

func estimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	// A conservative cross-language approximation: English-ish text trends near
	// four bytes per token, while CJK-heavy text is closer to one rune per token.
	bytes := len(s)
	runes := utf8.RuneCountInString(s)
	byBytes := (bytes + 3) / 4
	if runes > byBytes {
		return runes
	}
	return byBytes
}

// compact summarizes the older middle of the session and replaces it in place:
// the session becomes system + summary + recent tail. The dropped originals are
// archived first, so the full history stays traceable. trigger is "auto" (the
// window threshold) or "manual" (/compact); it rides the Compaction events so a
// frontend can label the card. instructions is optional extra summary guidance
// (the user's `/compact <focus>` text); a PreCompact hook can contribute more.
// force bypasses the fold-economics skip (manual /compact and the force-ratio
// high-water mark always compact). A Started event is emitted before the (network)
// summarize so the UI can show a "compacting…" placeholder, and a Done event
// (carrying the summary) replaces it.
func (a *Agent) compact(ctx context.Context, trigger, instructions string, force bool) error {
	msgs := a.session.Messages
	head, start, ok := a.planCompaction(msgs, minCompactMessages)
	if !ok {
		// A single huge message can still be worth folding. Keep the normal
		// message-count guard for small histories, but let content size decide
		// whether a one-message region has real compaction value.
		head, start, ok = a.planCompaction(msgs, 1)
	}
	if !ok {
		return nil // recent tail already covers everything worth keeping
	}
	region := msgs[head:start]

	// Base layer: every small user turn in the region is kept verbatim (the
	// deterministic floor — a fact the user stated is never summarized away,
	// wherever in the session they said it); only the rest folds into the digest.
	kept, fold := a.partitionFold(region)
	if len(fold) == 0 {
		return nil // nothing but kept user turns — a fold would save nothing
	}

	// Economic check on the foldable part (kept user turns don't count toward the
	// savings): skip if too small to justify the call, unless force demands it.
	if !force && !foldEconomics(fold) {
		return nil
	}

	a.sink.Emit(event.Event{Kind: event.CompactionStarted, Compaction: event.Compaction{Trigger: trigger}})

	// A PreCompact hook can steer what the summary keeps; its stdout joins any
	// explicit /compact <focus> text.
	if a.hooks != nil {
		if hookInstr := a.hooks.PreCompact(ctx, trigger); hookInstr != "" {
			if instructions != "" {
				instructions += "\n"
			}
			instructions += hookInstr
		}
	}

	archived := ""
	if a.archiveDir != "" {
		path, err := archiveMessages(a.archiveDir, fold)
		if err != nil {
			a.emitCompactionAborted(trigger)
			return fmt.Errorf("archive: %w", err)
		}
		archived = path
	}

	// The digest covers only the foldable work; kept user turns and prior digests
	// are spliced back verbatim, so a fact that reached a digest once is never
	// re-summarized away and the user's own words are never touched. Digests
	// accumulate (small) rather than collapsing into one lossy rolling summary.
	// Also pin the latest real user request from the fold region when it would
	// otherwise only live inside the summary (already-kept or still-in-tail
	// copies are left alone so large foldable pastes can still shrink).
	latestUser := latestRealUserRequest(fold)
	if latestUser != nil {
		// Already preserved in kept or still present in the recent tail.
		if messageInSlice(kept, *latestUser) || messageInSlice(msgs[start:], *latestUser) {
			latestUser = nil
		} else if !a.pinnableUserTurn(*latestUser) {
			// Large pasted user turns stay foldable; re-pinning them would undo
			// the compaction savings the fold was meant to buy.
			latestUser = nil
		}
	}
	summary, stage, attempts, prefixReused, failClass, err := a.summarizeLadder(ctx, fold, instructions)
	if err != nil {
		// Mechanical fold: the foldable region is already archived, so stand in a
		// deterministic marker rather than aborting. /compact then always frees
		// context (and auto-compaction can't loop on a still-full window); the
		// verbatim user turns kept above are untouched.
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Context was compacted without a generated summary.", Detail: "compaction summary unavailable (" + err.Error() + "); folded mechanically"})
		summary = mechanicalFoldDigest(len(fold), archived)
		stage = "mechanical"
		failClass = classifySummaryError(err)
	}

	summaryMsg := CompactionSummaryMessage(
		"Summary of earlier conversation (older messages were compacted to save context):\n" + summary,
	)
	// Ensure legacy prefixes remain for downgrade compatibility.
	if !strings.Contains(summaryMsg.Content, summaryTagOpen) {
		summaryMsg.Content = summaryTagOpen + "\n" + summaryMsg.Content + "\n" + summaryTagClose
	}

	var stateMsg *provider.Message
	var stateBytes int
	if a.compactionState != nil {
		st := a.compactionState.CompactionState()
		if archived != "" && st.ArchivePath == "" {
			st.ArchivePath = archived
		}
		body := RenderCompactionState(st)
		stateBytes = len(body)
		m := CompactionStateMessage(st)
		stateMsg = &m
	}

	compacted := make([]provider.Message, 0, head+len(kept)+3+len(msgs)-start)
	compacted = append(compacted, msgs[:head]...)
	compacted = append(compacted, kept...)
	if latestUser != nil {
		// Keep latest real user request verbatim after the pinned prefix/kept
		// facts and before the summary, so post-compaction always has the task text.
		compacted = append(compacted, *latestUser)
	}
	compacted = append(compacted, summaryMsg)
	if stateMsg != nil {
		compacted = append(compacted, *stateMsg)
	}
	compacted = append(compacted, msgs[start:]...)
	a.session.Replace(compacted)
	a.session.IncrementRewrite()

	a.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{
		Trigger:      trigger,
		Messages:     len(fold),
		Summary:      summary,
		Archive:      archived,
		Stage:        stage,
		Attempts:     attempts,
		PrefixReused: prefixReused,
		FailureClass: failClass,
		StateBytes:   stateBytes,
	}})
	return nil
}

// emitCompactionAborted resolves a "compacting…" placeholder when a pass fails
// after the Started event: a Done with no summary tells a frontend to drop the
// placeholder. The caller still surfaces the reason (a Notice), so this carries
// no text of its own.
func (a *Agent) emitCompactionAborted(trigger string) {
	a.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{Trigger: trigger}})
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
	summary, err := a.summarize(ctx, region, "")
	if err != nil {
		return err
	}
	next := make([]provider.Message, 0, fromIdx+1)
	next = append(next, msgs[:fromIdx]...)
	next = append(next, CompactionSummaryMessage("Summary of the later conversation (compacted from here on):\n"+summary))
	a.session.Replace(next)
	a.session.IncrementRewrite()
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
	summary, err := a.summarize(ctx, region, "")
	if err != nil {
		return err
	}
	next := make([]provider.Message, 0, head+1+len(msgs)-toIdx)
	next = append(next, msgs[:head]...)
	next = append(next, CompactionSummaryMessage("Summary of earlier conversation (compacted up to here):\n"+summary))
	next = append(next, msgs[toIdx:]...)
	a.session.Replace(next)
	a.session.IncrementRewrite()
	a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("summarized %d earlier messages → summary", len(region))})
	return nil
}

// IsCompactionSummary reports whether m is a rolling digest inserted by a
// prior compaction fold. Exported for session owners outside this package
// (e.g. the guardian) whose turn rollback must not treat a digest as a
// disposable user message.
func IsCompactionSummary(m provider.Message) bool { return isCompactionSummary(m) }

// isCompactionSummary reports whether m is a rolling summary from a prior fold.
func isCompactionSummary(m provider.Message) bool {
	if m.Role != provider.RoleUser {
		return false
	}
	if m.SyntheticReason == provider.SyntheticCompactionSummary {
		return true
	}
	return strings.HasPrefix(strings.TrimLeft(m.Content, "\n "), summaryTagOpen)
}

// isCompactionState reports whether m is the deterministic host state block.
func isCompactionState(m provider.Message) bool {
	if m.Role != provider.RoleUser {
		return false
	}
	if m.SyntheticReason == provider.SyntheticCompactionState {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(m.Content), "<compaction-state")
}

// pinnedPrefixLen counts the leading messages a fold keeps verbatim: the system
// prompt, the pinned session-context message (always), the first user turn when
// small enough, and any prior summaries — so a fold never summarizes the user's
// facts away, and a later fold never re-summarizes an earlier summary into
// nothing. A large first turn (pasted content) stays foldable so pinning never
// starves the window.
func (a *Agent) pinnedPrefixLen(msgs []provider.Message) int {
	i := 0
	if i < len(msgs) && msgs[i].Role == provider.RoleSystem {
		i++
	}
	// Session context is always pinned for session-context-v2 layouts.
	if i < len(msgs) && IsSessionContextMessage(msgs[i]) {
		i++
	}
	if i < len(msgs) && msgs[i].Role == provider.RoleUser && !isCompactionSummary(msgs[i]) && !isCompactionState(msgs[i]) && !IsSessionContextMessage(msgs[i]) && a.pinnableUserTurn(msgs[i]) {
		i++
	}
	for i < len(msgs) && (isCompactionSummary(msgs[i]) || isCompactionState(msgs[i])) {
		i++
	}
	return i
}

// latestRealUserRequest returns a copy of the newest user-authored (non-synthetic)
// message in region, or nil.
func latestRealUserRequest(region []provider.Message) *provider.Message {
	for i := len(region) - 1; i >= 0; i-- {
		m := region[i]
		if m.Role != provider.RoleUser {
			continue
		}
		if IsSyntheticUserMessage(m) || isCompactionSummary(m) || isCompactionState(m) {
			continue
		}
		if _, isSteer := SteerText(m.Content); isSteer {
			continue
		}
		cp := m
		return &cp
	}
	return nil
}

func messageInSlice(msgs []provider.Message, want provider.Message) bool {
	for _, m := range msgs {
		if m.Role == want.Role && m.Content == want.Content && m.SyntheticReason == want.SyntheticReason {
			return true
		}
	}
	return false
}

// pinnableUserTurn reports whether a user turn is small enough to keep verbatim. A
// turn larger than a brief (pasted content) folds like any other message so the
// kept-verbatim floor never starves the window.
func (a *Agent) pinnableUserTurn(m provider.Message) bool {
	budget := maxPinnedFirstUserTokens
	if a.contextWindow > 0 {
		if f := int(float64(a.contextWindow) * pinnedFirstUserWindowFrac); f < budget {
			budget = f
		}
	}
	return int(float64(msgChars(m))*a.tokPerChar()) <= budget
}

// partitionFold splits a compaction region into what is kept verbatim — small user
// turns (a fact the user stated is never summarized away) and prior digests (so a
// later fold never re-summarizes an earlier digest and drops the facts it already
// captured) — and the rest, which folds. Order within each group is preserved.
func (a *Agent) partitionFold(region []provider.Message) (kept, fold []provider.Message) {
	policyKeep := keepIndexes(region, a.keepPolicy)
	for i, m := range region {
		if policyKeep[i] || isCompactionSummary(m) || (m.Role == provider.RoleUser && a.pinnableUserTurn(m)) {
			kept = append(kept, m)
		} else {
			fold = append(fold, m)
		}
	}
	return kept, fold
}

func keepIndexes(region []provider.Message, policy KeepPolicy) []bool {
	keep := make([]bool, len(region))
	policyStart := 0
	for i, m := range region {
		if isCompactionSummary(m) {
			policyStart = i + 1
		}
	}
	// Retention applies only to messages since the latest digest; older kept
	// messages are allowed to fold on the next pass so they cannot grow forever.
	for i, m := range region {
		if i >= policyStart && shouldKeepMessage(m, policy) {
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
				keepToolCallGroup(region, keep, j)
			}
		case provider.RoleAssistant:
			keepToolCallGroup(region, keep, i)
		}
	}
	return keep
}

func keepToolCallGroup(region []provider.Message, keep []bool, assistantIndex int) {
	if assistantIndex < 0 || assistantIndex >= len(region) {
		return
	}
	m := region[assistantIndex]
	if m.Role != provider.RoleAssistant || len(m.ToolCalls) == 0 {
		return
	}
	keep[assistantIndex] = true
	ids := toolCallIDs(m)
	for j := assistantIndex + 1; j < len(region) && region[j].Role == provider.RoleTool; j++ {
		if ids[region[j].ToolCallID] {
			keep[j] = true
		}
	}
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
	if m.Role != provider.RoleUser {
		return false
	}
	content := strings.TrimSpace(strings.ToLower(m.Content))
	return strings.HasPrefix(content, "[[keep]]") ||
		strings.HasPrefix(content, "[keep]") ||
		strings.HasPrefix(content, "<keep>") ||
		strings.HasPrefix(content, "<!-- keep -->")
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

// planCompaction locates the region to summarize. head is the count of leading
// messages preserved verbatim (see pinnedPrefixLen); start is where the preserved
// recent tail begins, so msgs[head:start] is compacted. The tail is bounded by a
// token budget (not a message count), so a few large tool outputs can't keep it
// above the trigger and re-fire compaction every turn. ok is false when there is
// too little to compact.
func (a *Agent) planCompaction(msgs []provider.Message, min int) (head, start int, ok bool) {
	head = a.pinnedPrefixLen(msgs)
	if a.contextWindow > 0 {
		budget := defaultTailTokens
		if maxByWin := int(float64(a.contextWindow) * defaultCompactTarget); maxByWin < budget {
			budget = maxByWin
		}
		start = tailStart(msgs, head, budget, a.tokPerChar(), a.tailFloor())
	} else {
		// No window to budget against (manual /compact on an unconfigured
		// provider): keep a fixed count of recent messages, aligned off any tool.
		start = len(msgs) - a.tailFloor()
		for start > head && msgs[start].Role == provider.RoleTool {
			start--
		}
	}
	if start < head {
		start = head
	}
	if start-head < min {
		return head, start, false
	}
	return head, start, true
}

func (a *Agent) tailFloor() int {
	if a.recentKeep > minRecentKeep {
		return a.recentKeep
	}
	return minRecentKeep
}

// tailStart walks newest→oldest, growing the verbatim tail until the next
// message would push its token estimate past budgetTokens (but never below
// minKeep messages), then aligns the boundary back off any tool result so the
// tail never begins with an orphan whose assistant tool_calls were summarized
// away.
func tailStart(msgs []provider.Message, head, budgetTokens int, tokPerChar float64, minKeep int) int {
	start := len(msgs)
	acc := 0
	for i := len(msgs) - 1; i > head; i-- {
		c := int(float64(msgChars(msgs[i])) * tokPerChar)
		if len(msgs)-i > minKeep && acc+c > budgetTokens {
			break
		}
		acc += c
		start = i
	}
	// start == len(msgs) when nothing fit the tail (a session too small to have a
	// message after head); there is no msgs[start] to align off, and the caller's
	// minCompactMessages check then no-ops the pass.
	for start > head && start < len(msgs) && msgs[start].Role == provider.RoleTool {
		start--
	}
	return start
}

// tokPerChar derives a tokens-per-character ratio from the last turn's real
// usage so per-message estimates track the provider's tokenizer without a local
// one. Reasoning content is excluded from the char count to match the prompt
// actually sent (the provider strips it). Falls back to ~4 chars/token before
// any usage is known, and ignores absurd ratios.
func (a *Agent) tokPerChar() float64 {
	if u := a.lastUsage.Load(); u != nil && u.PromptTokens > 0 {
		if c := charsOfMessages(a.session.Messages); c > 0 {
			if r := float64(u.PromptTokens) / float64(c); r > 0.05 && r < 2 {
				return r
			}
		}
	}
	return fallbackTokPerChar
}

// msgChars counts the characters that ride to the provider for one message —
// content plus tool-call names and arguments, but not reasoning (stripped on
// send).
func msgChars(m provider.Message) int {
	n := len(m.Content)
	for _, tc := range m.ToolCalls {
		n += len(tc.Name) + len(tc.Arguments)
	}
	return n
}

func charsOfMessages(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		n += msgChars(m)
	}
	return n
}

// summarize asks the executor's own provider (no tools) to distill the region
// into a briefing, returning the collected text. instructions, when non-empty,
// is appended to the system prompt as extra focus guidance (from /compact <focus>
// and/or a PreCompact hook).
func (a *Agent) summarize(ctx context.Context, region []provider.Message, instructions string) (string, error) {
	return a.summarizeStage(ctx, region, instructions, "fitted_transcript", false)
}

// summarizeStage runs one summarization attempt at the given ladder stage.
// prefixReuse reuses the last provider-ready messages + identical tools with
// ToolChoiceNone when the provider supports it.
func (a *Agent) summarizeStage(ctx context.Context, region []provider.Message, instructions, stage string, prefixReuse bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, summaryTimeout)
	defer cancel()
	sys := summarySystemPrompt
	if strings.TrimSpace(instructions) != "" {
		sys += "\n\nAdditional focus for this compaction (prioritize keeping this):\n" + strings.TrimSpace(instructions)
	}

	var req provider.Request
	if prefixReuse && a.supportsToolChoiceNone() && len(a.lastProviderMsgs) > 0 {
		// Reuse the exact last provider-ready prefix and tool schemas; only append
		// one user turn that carries the full summarizer policy (and optional
		// focus). Do NOT re-embed the transcript — it is already in the prefix
		// and would push past the 80% window. ToolChoiceNone disables tools.
		msgs := append([]provider.Message(nil), a.lastProviderMsgs...)
		msgs = append(msgs, provider.Message{
			Role:    provider.RoleUser,
			Content: prefixReuseSummaryUserTurn(sys, instructions),
		})
		req = provider.Request{
			Messages:    msgs,
			Tools:       append([]provider.ToolSchema(nil), a.lastProviderTools...),
			Temperature: provider.OptionalTemperature(a.temperature),
			ToolChoice:  provider.ToolChoiceNone,
		}
	} else {
		req = provider.Request{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: sys},
				{Role: provider.RoleUser, Content: renderTranscriptForStage(region, stage)},
			},
			Temperature: provider.OptionalTemperature(a.temperature),
			ToolChoice:  provider.ToolChoiceNone,
		}
	}

	ch, err := a.prov.Stream(ctx, req)
	if err != nil {
		return "", err
	}

	// select on ctx.Done so a stalled stream (open but never delivering or closing)
	// unblocks on timeout instead of pinning the "compacting…" placeholder forever.
	var b strings.Builder
	var usage *provider.Usage
	emitUsage := func() {
		if usage != nil && usage.TotalTokens > 0 {
			a.sink.Emit(event.Event{Kind: event.Usage, Usage: usage, Pricing: a.pricing, UsageSource: event.UsageSourceCompaction})
		}
	}
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				emitUsage()
				s := strings.TrimSpace(b.String())
				if s == "" {
					return "", fmt.Errorf("summarizer returned empty output")
				}
				// Tool calls in a summary are a quality failure.
				if strings.Contains(s, "tool_calls") && strings.Contains(s, `"name"`) {
					return "", fmt.Errorf("summarizer returned tool call content")
				}
				return s, nil
			}
			switch chunk.Type {
			case provider.ChunkText:
				b.WriteString(chunk.Text)
			case provider.ChunkToolCall:
				return "", fmt.Errorf("summarizer attempted tool call")
			case provider.ChunkUsage:
				usage = chunk.Usage
			case provider.ChunkError:
				return "", chunk.Err
			}
		}
	}
}

// summarizeWithRetry retries one non-timeout failure (a transient stream drop or
// rate blip); a timeout or a second failure returns so the caller folds
// mechanically rather than waiting again.
func (a *Agent) summarizeWithRetry(ctx context.Context, fold []provider.Message, instructions string) (string, error) {
	summary, err := a.summarize(ctx, fold, instructions)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return summary, err
	}
	return a.summarize(ctx, fold, instructions)
}

// summarizeLadder tries up to three stages (prefix_reuse → fitted → minimal)
// with one extra transient retry. Auth/cancel go straight to mechanical fallback.
// Quality failures step down the ladder; a non-empty but sub-quality last-stage
// summary is still preferred over a purely mechanical fold so the model keeps
// some briefing when headings are incomplete.
func (a *Agent) summarizeLadder(ctx context.Context, fold []provider.Message, instructions string) (summary, stage string, attempts int, prefixReused bool, failureClass string, err error) {
	stages := []struct {
		name        string
		prefixReuse bool
	}{
		{"prefix_reuse", true},
		{"fitted_transcript", false},
		{"minimal_transcript", false},
	}
	inputChars := charsOfMessages(fold)
	var lastErr error
	var best string
	var bestStage string
	var bestPrefix bool
	transientUsed := false
	for _, st := range stages {
		if st.prefixReuse && !a.supportsToolChoiceNone() {
			continue
		}
		if st.prefixReuse && len(a.lastProviderMsgs) == 0 {
			continue
		}
		attempts++
		out, e := a.summarizeStage(ctx, fold, instructions, st.name, st.prefixReuse)
		if e != nil {
			lastErr = e
			fc := classifySummaryError(e)
			if fc == "auth" || fc == "cancel" {
				return "", st.name, attempts, st.prefixReuse, fc, e
			}
			if fc == "transient" && !transientUsed {
				transientUsed = true
				attempts++
				out, e = a.summarizeStage(ctx, fold, instructions, st.name, st.prefixReuse)
				if e == nil {
					if summaryQualityOK(out, inputChars) {
						return out, st.name, attempts, st.prefixReuse, "", nil
					}
					if len(out) > len(best) {
						best, bestStage, bestPrefix = out, st.name, st.prefixReuse
					}
					lastErr = fmt.Errorf("summary failed quality gate")
					failureClass = "quality"
				} else {
					lastErr = e
					if classifySummaryError(e) == "auth" || classifySummaryError(e) == "cancel" {
						return "", st.name, attempts, st.prefixReuse, classifySummaryError(e), e
					}
				}
			}
			// context-too-long and other errors fall through to next stage
			continue
		}
		if !summaryQualityOK(out, inputChars) {
			lastErr = fmt.Errorf("summary failed quality gate")
			failureClass = "quality"
			if len(out) > len(best) {
				best, bestStage, bestPrefix = out, st.name, st.prefixReuse
			}
			continue
		}
		return out, st.name, attempts, st.prefixReuse, "", nil
	}
	if strings.TrimSpace(best) != "" {
		// Prefer the best non-empty model summary over a mechanical fold.
		return best, bestStage, attempts, bestPrefix, "quality", nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all summary stages failed")
	}
	if failureClass == "" {
		failureClass = classifySummaryError(lastErr)
	}
	return "", "mechanical", attempts, false, failureClass, lastErr
}

func (a *Agent) supportsToolChoiceNone() bool {
	// OpenAI-compatible and Anthropic adapters both support ToolChoiceNone.
	// Unknown providers still receive the field; if they reject it, classify as
	// context/transient and fall through. We optimistically allow when the
	// provider kind is empty (tests/mocks).
	return true
}

func summaryQualityOK(s string, inputChars int) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	minChars := inputChars / 20
	if minChars < 200 {
		minChars = 200
	}
	if minChars > 500 {
		minChars = 500
	}
	// Short folds cannot meet a large min; floor against input size.
	if inputChars > 0 && inputChars < minChars {
		minChars = inputChars / 2
		if minChars < 40 {
			minChars = 40
		}
	}
	if len(s) < minChars {
		return false
	}
	required := []string{"## Goal", "## Completed & decisions", "## Current state", "## Pending & next step"}
	// Accept legacy heading set used by older digests still in the wild.
	legacyOK := strings.Contains(s, "## Goal") && strings.Contains(s, "## Pending & next step") &&
		(strings.Contains(s, "## Decisions") || strings.Contains(s, "## Standing facts"))
	for _, h := range required {
		if !strings.Contains(s, h) {
			return legacyOK
		}
	}
	return true
}

func classifySummaryError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancel"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "transient"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"), strings.Contains(msg, "auth"), strings.Contains(msg, "unauthorized"), strings.Contains(msg, "api key"):
		return "auth"
	case strings.Contains(msg, "413"), strings.Contains(msg, "context"), strings.Contains(msg, "too long"), strings.Contains(msg, "maximum context"), strings.Contains(msg, "token"):
		if strings.Contains(msg, "422") || strings.Contains(msg, "400") || strings.Contains(msg, "413") || strings.Contains(msg, "context") || strings.Contains(msg, "too long") {
			return "context"
		}
	case strings.Contains(msg, "quality"):
		return "quality"
	case strings.Contains(msg, "empty"):
		return "empty"
	}
	if strings.Contains(msg, "400") || strings.Contains(msg, "422") {
		return "context"
	}
	return "transient"
}

// prefixReuseSummaryUserTurn is appended to the last provider-ready messages
// during prefix_reuse. It carries the full quality headings and optional focus
// without re-sending the transcript (already present in the reused prefix).
func prefixReuseSummaryUserTurn(sys, instructions string) string {
	var b strings.Builder
	b.WriteString("Compact the earlier conversation already present above into a structured briefing. ")
	b.WriteString("Do not call tools. Follow these exact requirements:\n\n")
	b.WriteString(sys)
	if strings.TrimSpace(instructions) != "" {
		b.WriteString("\n\nAdditional focus for this compaction (prioritize keeping this):\n")
		b.WriteString(strings.TrimSpace(instructions))
	}
	return b.String()
}

func renderTranscriptForStage(msgs []provider.Message, stage string) string {
	switch stage {
	case "minimal_transcript":
		return renderMinimalTranscript(msgs)
	case "fitted_transcript", "prefix_reuse":
		// prefix_reuse does not use this path; fitted does.
		return renderFittedTranscript(msgs)
	default:
		return renderTranscript(msgs)
	}
}

// renderFittedTranscript replaces large tool results with name/status/head/tail/digest.
func renderFittedTranscript(msgs []provider.Message) string {
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
				fmt.Fprintf(&b, "[assistant calls %s] %s\n", tc.Name, summarizeToolArgs(tc.Arguments))
			}
			b.WriteString("\n")
		case provider.RoleTool:
			fmt.Fprintf(&b, "[tool %s result]\n%s\n\n", m.Name, fitToolResult(m.Content))
		case provider.RoleSystem:
			fmt.Fprintf(&b, "[system]\n%s\n\n", m.Content)
		}
	}
	return b.String()
}

func renderMinimalTranscript(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			if IsSyntheticUserMessage(m) {
				continue
			}
			fmt.Fprintf(&b, "[user]\n%s\n\n", m.Content)
		case provider.RoleAssistant:
			if m.Content != "" {
				// Keep assistant conclusions only (first ~500 chars).
				c := m.Content
				if len(c) > 500 {
					c = c[:500] + "…"
				}
				fmt.Fprintf(&b, "[assistant]\n%s\n", c)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[tool %s args_digest %s]\n", tc.Name, shortDigest(tc.Arguments))
			}
			b.WriteString("\n")
		case provider.RoleTool:
			line := strings.SplitN(strings.TrimSpace(m.Content), "\n", 2)[0]
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			fmt.Fprintf(&b, "[tool %s] %s\n\n", m.Name, line)
		}
	}
	return b.String()
}

func fitToolResult(content string) string {
	const headTail = 400
	ok := !strings.HasPrefix(strings.TrimSpace(strings.ToLower(content)), "error:")
	status := "ok"
	if !ok {
		status = "error"
	}
	if len(content) <= headTail*2+64 {
		return content
	}
	head := content
	if len(head) > headTail {
		head = content[:headTail]
	}
	tail := content[len(content)-headTail:]
	return fmt.Sprintf("status=%s chars=%d digest=%s\nHEAD:\n%s\n…\nTAIL:\n%s",
		status, len(content), shortDigest(content), head, tail)
}

func shortDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// mechanicalFoldDigest is the deterministic stand-in used when the summarizer is
// unreachable: the foldable region is already archived, so the digest just notes
// the gap and points the model at the user for anything it needs from before it.
func mechanicalFoldDigest(n int, archive string) string {
	where := "."
	if archive != "" {
		where = " (archived to " + archive + ")."
	}
	return fmt.Sprintf("%d earlier message(s) were folded here to free context, but the automatic summary was unavailable%s Ask the user if you need details from before this point.", n, where)
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
				fmt.Fprintf(&b, "[assistant calls %s] %s\n", tc.Name, summarizeToolArgs(tc.Arguments))
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

// summarizeToolArgs returns a short summary of tool-call arguments instead of
// the full JSON. This prevents the summarizer from reproducing long argument
// text (like sub-agent task prompts) in the compaction summary, which would
// leak into the session as a user message (#4317).
func summarizeToolArgs(args string) string {
	if args == "" {
		return "(no arguments)"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		// Not valid JSON — return a length hint instead of raw text.
		return fmt.Sprintf("(%d bytes)", len(args))
	}
	keys := make([]string, 0, len(parsed))
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("{%s} (%d keys)", strings.Join(keys, ", "), len(parsed))
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
