package agent

import (
	"errors"
	"fmt"
	"time"

	"reasonix/internal/provider"
)

type summaryProjectionCommit struct {
	canonical, fold, projected                       []provider.Message
	result                                           foldSummary
	transcriptVersion, projectionVersion, generation uint64
	activeTurn                                       int64
	trigger, summary, inputHash, outputHash          string
	sourceTokens, projectionTokens                   int
}

// commitSummaryProjection performs the final CAS and durable sidecar switch
// after all network and interceptor work has completed. Canonical history is
// already the lossless archive, so checkpoint installation creates no copy.
func (a *Agent) commitSummaryProjection(commit summaryProjectionCommit) (CompactionState, error) {
	current, currentVersion := a.session.snapshotMessagesVersion()
	a.compactionMu.Lock()
	currentProjectionVersion := a.compactionState.Projection.ProjectionVersion
	currentGeneration := a.compactionState.Generation
	a.compactionMu.Unlock()
	if currentVersion != commit.transcriptVersion || len(current) != len(commit.canonical) ||
		coveredPrefixHash(current, len(current)) != coveredPrefixHash(commit.canonical, len(commit.canonical)) ||
		currentProjectionVersion != commit.projectionVersion || currentGeneration != commit.generation {
		return CompactionState{}, errCompressStaleContext
	}

	state := a.summaryProjectionState(commit)
	if err := a.installProjectionIfCurrent(state, commit.projectionVersion, commit.generation); err != nil {
		if errors.Is(err, errCompressStaleContext) {
			return CompactionState{}, err
		}
		return CompactionState{}, fmt.Errorf("persist projection: %w", err)
	}
	if commit.activeTurn != 0 && commit.trigger != CompactionTriggerManual {
		a.lastCompactionTurn.Store(commit.activeTurn)
	}
	a.emitContextMaintenance(state.LastReceipt)
	return state, nil
}

func (a *Agent) summaryProjectionState(commit summaryProjectionCommit) CompactionState {
	projectionVersion := commit.projectionVersion + 1
	now := time.Now().UTC()
	summaryHash := summaryContentHash(commit.summary)
	coveredHash := coveredPrefixHash(commit.canonical, len(commit.canonical))
	receipt := &ContextMaintenanceReceipt{
		OperationID: fmt.Sprintf("summary-%d-%s", projectionVersion, commit.outputHash), Status: "applied",
		Action: "summary", Trigger: commit.trigger, SourceProjection: commit.projectionVersion,
		ProjectionVersion: projectionVersion, CoveredCount: len(commit.canonical), CoveredPrefixHash: coveredHash,
		InputHash: commit.inputHash, OutputHash: commit.outputHash, InputTokens: commit.sourceTokens,
		ResultTokens: commit.projectionTokens, SavedTokens: max(0, commit.sourceTokens-commit.projectionTokens),
		SummaryHash: summaryHash, CacheBreak: true, CreatedAt: now,
	}
	// Schema v3 keeps projection + receipt primary. last_trigger / last_mode /
	// last_*_tokens remain for status/report surfaces that still read them.
	return CompactionState{
		SchemaVersion: compactionStateSchemaCurrent, TranscriptVersion: commit.transcriptVersion,
		Generation: commit.generation + 1, PromptCacheKey: a.currentPromptCacheKey(),
		Projection: ContextProjection{
			Messages: commit.projected, TranscriptVersion: commit.transcriptVersion,
			ProjectionVersion: projectionVersion, CoveredCount: len(commit.canonical), CoveredPrefixHash: coveredHash,
			SummaryHash: summaryHash, SourceTokens: commit.sourceTokens, ProjectionTokens: commit.projectionTokens,
			ViewInputHash: commit.inputHash, ViewOutputHash: commit.outputHash, CreatedAt: now,
		},
		LastTrigger: commit.trigger, LastMode: CompactionModeSummarized,
		LastSourceTokens: commit.sourceTokens, LastResultTokens: commit.projectionTokens,
		LastReceipt: receipt, UpdatedAt: now,
	}
}
