package sessionv3

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

var ErrImportConflict = errors.New("legacy transcript and session event source conflict")

type ImportResult struct {
	TargetID string
	Source   Source
	Reused   bool
	Kind     string
}

func importSourceForLegacy(ctx context.Context, sourcePath, targetRoot, headID string) (ImportResult, error) {
	// Freeze and parse every candidate before publication. Inspecting a paired
	// sidecar after publishing legacy history can omit newer work and leave an
	// adopted target behind after a refused import.
	frozenLegacy, err := freezeLegacyHead(ctx, sourcePath, headID, true)
	if err != nil {
		return ImportResult{}, err
	}
	legacy := frozenLegacy.messages

	previewDir := filepath.Join(targetRoot, agent.BranchID(sourcePath))
	frozenPreview, err := freezePairedPreview(ctx, previewDir)
	if errors.Is(err, fs.ErrNotExist) {
		// No paired sidecar (or no target root yet) means the transcript is the
		// only candidate. Nothing has been published at this point.
		return publishLegacyImport(ctx, frozenLegacy, targetRoot)
	}
	if err != nil {
		return ImportResult{}, fmt.Errorf("inspect paired session events: %w", err)
	}
	preview, meaningful, err := inspectFrozenPreview(ctx, frozenPreview)
	if err != nil {
		return ImportResult{}, fmt.Errorf("inspect paired session events: %w", err)
	}
	if !meaningful {
		return publishLegacyImport(ctx, frozenLegacy, targetRoot)
	}

	switch {
	case messagesEqual(legacy, preview), messagesPrefix(legacy, preview):
		// The event sidecar carries the same history or a strictly longer one,
		// so it is the only source that can be resumed without losing work.
		imported, importErr := importFrozenPreview(ctx, frozenPreview, targetRoot)
		return ImportResult{TargetID: imported.TargetID, Source: imported.Source, Reused: imported.Reused, Kind: "events"}, importErr
	case messagesPrefix(preview, legacy):
		// The transcript is strictly newer; the sidecar is an earlier prefix.
		return publishLegacyImport(ctx, frozenLegacy, targetRoot)
	default:
		// Neither source is a provable prefix of the other. Both originals stay
		// read-only and no executable target is created.
		left, right := comparableImportMessages(legacy), comparableImportMessages(preview)
		return ImportResult{}, fmt.Errorf("%w: legacy=%s events=%s legacy_messages=%d event_messages=%d first_difference=%d", ErrImportConflict, sourcePath, frozenPreview.source.Version, len(left), len(right), firstMessageDifference(left, right))
	}
}

// publishLegacyImport materializes the transcript target. It runs only after
// the source decision is final, so a refused or sidecar-winning import never
// creates the legacy target as a side effect.
func publishLegacyImport(ctx context.Context, frozen *frozenLegacyHead, targetRoot string) (ImportResult, error) {
	migration, err := frozen.publish(ctx, targetRoot)
	if err != nil {
		return ImportResult{}, err
	}
	return importResultFromLegacy(migration), nil
}

func importResultFromLegacy(result MigrationResult) ImportResult {
	return ImportResult{TargetID: result.TargetID, Source: result.Source, Reused: result.Reused, Kind: "legacy"}
}

func inspectFrozenPreview(ctx context.Context, frozen frozenPreview) ([]provider.Message, bool, error) {
	projection := Projection{}
	meaningful := false
	var projectionErr error
	knownKinds := ProjectionKinds
	if frozen.manifest.Codec == PrototypeCodec {
		knownKinds = PrototypeProjectionKinds
	}
	file, err := os.CreateTemp("", "reasonix-preview-events-*.jsonl")
	if err != nil {
		return nil, false, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(frozen.eventBytes); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	err = scanCommitFileCodec(file, 0, 1, frozen.manifest.Codec, knownKinds, func(_ int64, commit Commit) bool {
		if ctx.Err() != nil {
			return false
		}
		converted := cloneCommit(commit)
		for i := range converted.Events {
			kind := converted.Events[i].Kind
			if frozen.manifest.Codec == PrototypeCodec && kind == "context/replace" {
				converted.Events[i].Kind = "history/replace"
				kind = "history/replace"
			}
			switch kind {
			case "session/config", "session/title", "diagnostic":
			default:
				meaningful = true
			}
		}
		if applyErr := applyProjectionCommit(&projection, converted); applyErr != nil {
			projectionErr = applyErr
			return false
		}
		return true
	})
	_ = file.Close()
	if err != nil {
		return nil, false, err
	}
	if projectionErr != nil {
		return nil, false, projectionErr
	}
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	return projection.Messages, meaningful, nil
}

func messagesEqual(left, right []provider.Message) bool {
	return messageSequenceEqual(comparableImportMessages(left), comparableImportMessages(right))
}

func messagesPrefix(prefix, whole []provider.Message) bool {
	prefix = comparableImportMessages(prefix)
	whole = comparableImportMessages(whole)
	return len(prefix) <= len(whole) && messageSequenceEqual(prefix, whole[:len(prefix)])
}

func messageSequenceEqual(left, right []provider.Message) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !reflect.DeepEqual(left[i], right[i]) {
			return false
		}
	}
	return true
}

// comparableImportMessages keeps stable identity and provider-visible work but
// removes process-local diagnostics. Legacy snapshots may record a completed
// tool recovery receipt after the typed event mirror has already committed the
// same call; that metadata cannot authorize resumed execution and must not turn
// identical work into a migration conflict.
func comparableImportMessages(messages []provider.Message) []provider.Message {
	messages = provider.ModelMessages(messages)
	out := append([]provider.Message(nil), messages...)
	for i := range out {
		out[i].MemoryCitations = nil
		out[i].WorkDurationMs = 0
		out[i].CreatedAt = 0
		out[i].Edited = false
		out[i].Original = ""
	}
	return out
}

func firstMessageDifference(left, right []provider.Message) int {
	limit := min(len(left), len(right))
	for i := range limit {
		if !reflect.DeepEqual(left[i], right[i]) {
			return i
		}
	}
	return limit
}
