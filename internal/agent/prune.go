package agent

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Legacy snip helpers still support compatibility storage. Their public APIs
// are no-ops; pressure-time Harness pruning uses the rune-based policy below.
const (
	snippedMarker = "[snipped tool result — "
	prunedMarker  = "[elided tool result — "
	minPruneBytes = 1024

	toolPruneThresholdRunes = 8192
	toolPruneHeadRunes      = 4096
	toolPruneTailRunes      = 1024
	toolPruneMarker         = "[... tool result middle pruned ...]"
)

func pruneToolResultContent(content string) (string, bool) {
	runes := []rune(content)
	if len(runes) <= toolPruneThresholdRunes {
		return content, false
	}
	return string(runes[:toolPruneHeadRunes]) + toolPruneMarker + string(runes[len(runes)-toolPruneTailRunes:]), true
}

// pruneToolResultsToProjectionLocked installs a durable, model-visible prune
// projection. The caller owns compactionRunMu for the whole maintenance run;
// canonical storage, including RawContent, is never modified.
func (a *Agent) pruneToolResultsToProjectionLocked(trigger string) (bool, error) {
	canonical, transcriptVersion := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	stateSnapshot := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	visible, _ := a.visibleInputForFold(stateSnapshot, canonical, transcriptVersion)
	projected := append([]provider.Message(nil), visible...)
	affected := 0
	for i := range projected {
		if projected[i].Role != provider.RoleTool {
			continue
		}
		source := projected[i].Content
		if projected[i].RawContent != "" {
			source = projected[i].RawContent
		}
		if projected[i].ProviderContent != "" {
			source = projected[i].ProviderContent
		}
		if pruned, changed := pruneToolResultContent(source); changed {
			projected[i].Content = pruned
			projected[i].RawContent = ""
			projected[i].ProviderContent = ""
			affected++
		}
	}
	if affected == 0 {
		return false, nil
	}
	projected = provider.ProjectionMessages(projected)
	sourceTokens := a.estimatedVisibleRequestTokens(visible)
	resultTokens := a.estimatedVisibleRequestTokens(projected)
	inputHash := a.contextMaintenanceInputHash(modelInputMessages(visible))
	outputHash := providerVisibleFingerprint(modelInputMessages(projected))
	projectionVersion := stateSnapshot.Projection.ProjectionVersion + 1
	now := time.Now().UTC()
	coveredHash := coveredPrefixHash(canonical, len(canonical))
	receipt := &ContextMaintenanceReceipt{
		OperationID: fmt.Sprintf("prune-%d-%s", projectionVersion, outputHash), Status: "applied", Action: "prune",
		Trigger: trigger, SourceProjection: stateSnapshot.Projection.ProjectionVersion, ProjectionVersion: projectionVersion,
		CoveredCount: len(canonical), CoveredPrefixHash: coveredHash, InputHash: inputHash, OutputHash: outputHash,
		InputTokens: sourceTokens, ResultTokens: resultTokens, SavedTokens: max(0, sourceTokens-resultTokens),
		AffectedToolResults: affected, CacheBreak: true, CreatedAt: now,
	}
	next := stateSnapshot
	next.SchemaVersion = compactionStateSchemaCurrent
	next.TranscriptVersion = transcriptVersion
	next.Generation++
	next.PromptCacheKey = a.currentPromptCacheKey()
	next.Projection = ContextProjection{
		Messages: projected, TranscriptVersion: transcriptVersion, ProjectionVersion: projectionVersion,
		CoveredCount: len(canonical), CoveredPrefixHash: coveredHash, SourceTokens: sourceTokens,
		ProjectionTokens: resultTokens, ViewInputHash: inputHash, ViewOutputHash: outputHash, CreatedAt: now,
	}
	next.LastReceipt = receipt
	next.UpdatedAt = now

	a.sess.compactionMu.Lock()
	current, currentVersion := a.sess.conversation.snapshotMessagesVersion()
	if currentVersion != transcriptVersion || len(current) != len(canonical) ||
		coveredPrefixHash(current, len(current)) != coveredHash ||
		a.sess.compactionState.Projection.ProjectionVersion != stateSnapshot.Projection.ProjectionVersion ||
		a.sess.compactionState.Generation != stateSnapshot.Generation {
		a.sess.compactionMu.Unlock()
		return false, errCompressStaleContext
	}
	previous := a.sess.compactionState
	a.sess.compactionState = next
	if err := a.persistCompactionStateLocked(); err != nil {
		a.sess.compactionState = previous
		a.sess.compactionMu.Unlock()
		if errors.Is(err, errCompressStaleContext) {
			return false, err
		}
		return false, fmt.Errorf("persist prune projection: %w", err)
	}
	a.sess.checkpointState = "applied"
	a.sess.compactionMu.Unlock()
	a.emitContextMaintenance(receipt)
	return true, nil
}

type toolResultMaintenanceMode int

const (
	toolResultSnip toolResultMaintenanceMode = iota
	toolResultPrune
)

// PruneStats reports one maintenance pass.
type PruneStats struct {
	Results    int
	SavedChars int
	Archive    string
	Mode       toolResultMaintenanceMode
	InputHash  string
	Force      bool
}

// SnipStaleToolResults is a no-op: automatic prune/snip projections are gone.
func (a *Agent) SnipStaleToolResults() (PruneStats, error) {
	return PruneStats{Mode: toolResultSnip}, nil
}

// PruneStaleToolResults is a no-op: automatic prune/snip projections are gone.
func (a *Agent) PruneStaleToolResults() (PruneStats, error) {
	return PruneStats{Mode: toolResultPrune}, nil
}

func snipToolResult(m provider.Message, archive string, strategy snipStrategy) string {
	if archive == "" {
		archive = "the canonical transcript"
	}
	lines := strings.Split(m.Content, "\n")
	if len(lines) <= strategy.head+strategy.tail {
		headChars := minInt(strategy.headChars, len(m.Content)/2)
		tailChars := minInt(strategy.tailChars, len(m.Content)/4)
		return fmt.Sprintf("%s%s, %d bytes; full original retained in %s; single large line truncated]\n%s\n[... %d bytes omitted ...]\n%s",
			snippedMarker, m.Name, len(m.Content), archive,
			firstRunes(m.Content, headChars),
			omittedBytes(m.Content, headChars, tailChars),
			lastRunes(m.Content, tailChars))
	}
	head := strings.Join(lines[:strategy.head], "\n")
	tail := strings.Join(lines[len(lines)-strategy.tail:], "\n")
	return fmt.Sprintf("%s%s, %d bytes; full original retained in %s; showing first %d lines and last %d lines]\n%s\n[... %d lines omitted ...]\n%s",
		snippedMarker, m.Name, len(m.Content), archive, strategy.head, strategy.tail,
		head, len(lines)-strategy.head-strategy.tail, tail)
}

type snipStrategy struct {
	head      int
	tail      int
	headChars int
	tailChars int
}

var (
	defaultReadOnlySnip      = snipStrategy{head: 80, tail: 12, headChars: 10000, tailChars: 2000}
	defaultSideEffectingSnip = snipStrategy{head: 40, tail: 40, headChars: 8000, tailChars: 8000}
)

func (a *Agent) snipStrategyFor(name string) snipStrategy {
	if a.svc.tools != nil {
		if t, ok := a.svc.tools.Get(name); ok {
			if h, ok := t.(tool.SnipHinter); ok {
				return snipStrategyFromHint(h.SnipHint())
			}
			if t.ReadOnly() {
				return defaultReadOnlySnip
			}
			return defaultSideEffectingSnip
		}
	}
	return defaultReadOnlySnip
}

func snipStrategyFromHint(h tool.SnipHint) snipStrategy {
	return snipStrategy{head: h.Head, tail: h.Tail, headChars: h.HeadChars, tailChars: h.TailChars}
}

func firstRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneBoundary(s, n) {
		n--
	}
	return s[:n]
}

func lastRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && !isRuneBoundary(s, start) {
		start++
	}
	return s[start:]
}

func omittedBytes(s string, head, tail int) int {
	omitted := len(s) - head - tail
	if omitted < 0 {
		return 0
	}
	return omitted
}

func isRuneBoundary(s string, i int) bool {
	return i == 0 || i == len(s) || (i > 0 && i < len(s) && (s[i]&0xc0) != 0x80)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
