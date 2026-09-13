package sessionv3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/fileutil"
)

type frozenPreview struct {
	dir           string
	manifestBytes []byte
	eventBytes    []byte
	manifest      Manifest
	source        Source
}

type PrototypeImportResult struct {
	TargetID       string
	TargetDir      string
	Source         Source
	Reused         bool
	ImportedEvents uint64
}

// ImportPrototype performs the deliberately narrow bridge from the sidecar
// prototype codec to the final linear codec. It accepts only the complete,
// validated event prefix. Unknown required events and damaged complete records
// fail closed; an unterminated tail is preserved with the frozen source.
func ImportPrototype(ctx context.Context, sourceDir, targetRoot string) (PrototypeImportResult, error) {
	return importPreview(ctx, sourceDir, targetRoot)
}

// importPreview accepts both retired prototype codecs produced before the
// identity cutover. Callers must resolve it together with the paired legacy
// transcript; opening either source in isolation can silently drop newer work.
func importPreview(ctx context.Context, sourceDir, targetRoot string) (PrototypeImportResult, error) {
	if err := ctx.Err(); err != nil {
		return PrototypeImportResult{}, err
	}
	sourceDir = filepath.Clean(strings.TrimSpace(sourceDir))
	targetRoot = filepath.Clean(strings.TrimSpace(targetRoot))
	if sourceDir == "." || targetRoot == "." {
		return PrototypeImportResult{}, fmt.Errorf("sessionv3: prototype source and target root are required")
	}
	frozen, err := freezePreview(ctx, sourceDir)
	if err != nil {
		return PrototypeImportResult{}, err
	}
	return importFrozenPreview(ctx, frozen, targetRoot)
}

func freezePreview(ctx context.Context, sourceDir string) (frozenPreview, error) {
	return freezePreviewCodec(ctx, sourceDir, false)
}

// freezePairedPreview also accepts a current-codec directory because early
// v3 integrations derived that directory from a legacy transcript path while
// using whatever codec the binary considered current. Only the paired import
// resolver may treat such a store as an import source; normal current-codec
// sessions must continue to open by immutable session ID.
func freezePairedPreview(ctx context.Context, sourceDir string) (frozenPreview, error) {
	return freezePreviewCodec(ctx, sourceDir, true)
}

func freezePreviewCodec(ctx context.Context, sourceDir string, allowCurrent bool) (frozenPreview, error) {
	if _, err := os.Stat(sourceDir); err != nil {
		// Report absence before taking any lock. The ownership lock lives beside
		// the directory, so a missing candidate must not surface as a lock error
		// that callers cannot classify as "no paired source".
		return frozenPreview{}, err
	}
	// Prepare never waits behind a live writer. After the host suspends its own
	// producer, any remaining owner makes this import ineligible.
	releaseDirectory, err := filelock.TryAcquireMode(directoryOwnershipPath(sourceDir), filelock.ModeShared)
	if err != nil {
		if errors.Is(err, filelock.ErrHeld) {
			return frozenPreview{}, fmt.Errorf("%w: freeze preview ownership", ErrWriterOwned)
		}
		return frozenPreview{}, fmt.Errorf("freeze preview ownership: %w", err)
	}
	defer releaseDirectory()
	info, err := os.Stat(sourceDir)
	if err != nil {
		return frozenPreview{}, err
	}
	if !info.IsDir() {
		return frozenPreview{}, fmt.Errorf("sessionv3: preview path is not a directory: %s", sourceDir)
	}
	releaseWriter, err := filelock.TryAcquireMode(filepath.Join(sourceDir, "writer.lock"), filelock.ModeShared)
	if err != nil {
		if errors.Is(err, filelock.ErrHeld) {
			return frozenPreview{}, fmt.Errorf("%w: freeze preview writer", ErrWriterOwned)
		}
		return frozenPreview{}, fmt.Errorf("freeze preview writer: %w", err)
	}
	defer releaseWriter()
	manifestBytes, err := os.ReadFile(filepath.Join(sourceDir, "manifest.json"))
	if err != nil {
		return frozenPreview{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return frozenPreview{}, fmt.Errorf("%w: prototype manifest: %w", ErrDamagedStore, err)
	}
	knownCodec := manifest.Codec == PrototypeCodec || manifest.Codec == LegacyLinearCodec || allowCurrent && manifest.Codec == Codec
	if manifest.SchemaVersion != SchemaVersion || !knownCodec || strings.TrimSpace(manifest.SessionID) == "" {
		return frozenPreview{}, fmt.Errorf("%w: unsupported preview codec %q", ErrUnsupportedVersion, manifest.Codec)
	}
	eventBytes, err := os.ReadFile(filepath.Join(sourceDir, "events.jsonl"))
	if os.IsNotExist(err) {
		eventBytes = nil
	} else if err != nil {
		return frozenPreview{}, err
	}
	digest := sha256.New()
	digest.Write(manifestBytes)
	digest.Write([]byte{0})
	digest.Write(eventBytes)
	sourceDigest := hex.EncodeToString(digest.Sum(nil))
	source := Source{Path: sourceDir, Size: int64(len(manifestBytes) + len(eventBytes)), SHA256: sourceDigest, Version: manifest.Codec}
	return frozenPreview{dir: sourceDir, manifestBytes: manifestBytes, eventBytes: eventBytes, manifest: manifest, source: source}, nil
}

func importFrozenPreview(ctx context.Context, frozen frozenPreview, targetRoot string) (PrototypeImportResult, error) {
	prototype, source := frozen.manifest, frozen.source
	manifestBytes, eventBytes, sourceDir := frozen.manifestBytes, frozen.eventBytes, frozen.dir
	targetID := deterministicID("prototype-import\x00" + prototype.Codec + "\x00" + sourceDir + "\x00" + source.SHA256)
	targetDir := filepath.Join(targetRoot, targetID)
	result := PrototypeImportResult{TargetID: targetID, TargetDir: targetDir, Source: source}
	if existing, readErr := readManifest(filepath.Join(targetDir, "manifest.json")); readErr == nil {
		if existing.Source != nil && existing.Source.Path == source.Path && existing.Source.SHA256 == source.SHA256 && existing.Source.Version == prototype.Codec {
			result.Reused = true
			result.ImportedEvents = existing.InheritedEvents
			return result, nil
		}
		return PrototypeImportResult{}, fmt.Errorf("%w: prototype target %s has another source", ErrSessionExists, targetID)
	} else if !os.IsNotExist(readErr) {
		return PrototypeImportResult{}, readErr
	}
	if err := os.MkdirAll(targetRoot, 0o700); err != nil {
		return PrototypeImportResult{}, err
	}
	tmp, err := os.MkdirTemp(targetRoot, "."+targetID+".prototype-")
	if err != nil {
		return PrototypeImportResult{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tmp)
		}
	}()
	legacyDir := filepath.Join(tmp, "legacy", "prototype")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		return PrototypeImportResult{}, err
	}
	if err := fileutil.AtomicWriteFileStrict(filepath.Join(legacyDir, "manifest.json"), manifestBytes, 0o600); err != nil {
		return PrototypeImportResult{}, err
	}
	if err := fileutil.AtomicWriteFileStrict(filepath.Join(legacyDir, "events.jsonl"), eventBytes, 0o600); err != nil {
		return PrototypeImportResult{}, err
	}

	frozenLog, err := os.Open(filepath.Join(legacyDir, "events.jsonl"))
	if err != nil {
		return PrototypeImportResult{}, err
	}
	prototypeCommits := []Commit{}
	knownKinds := ProjectionKinds
	if prototype.Codec == PrototypeCodec {
		knownKinds = PrototypeProjectionKinds
	}
	err = scanCommitFileCodec(frozenLog, 0, 1, prototype.Codec, knownKinds, func(_ int64, commit Commit) bool {
		prototypeCommits = append(prototypeCommits, cloneCommit(commit))
		return true
	})
	_ = frozenLog.Close()
	if err != nil {
		return PrototypeImportResult{}, err
	}

	finalLog, lastSequence, err := convertPrototypeCommits(prototypeCommits, prototype.SessionID, targetID, prototype.Codec)
	if err != nil {
		return PrototypeImportResult{}, err
	}
	finalManifest := Manifest{
		SchemaVersion: SchemaVersion, Codec: Codec, SessionID: targetID,
		CreatedAt: time.Now().UTC(), InheritedEvents: lastSequence, Source: &source,
	}
	if err := writeManifestFile(filepath.Join(tmp, "manifest.json"), finalManifest); err != nil {
		return PrototypeImportResult{}, err
	}
	if err := fileutil.AtomicWriteFileStrict(filepath.Join(tmp, "events.jsonl"), finalLog, 0o600); err != nil {
		return PrototypeImportResult{}, err
	}
	if replayed, replayErr := Replay(tmp, nil); replayErr != nil || len(replayed) != len(prototypeCommits) {
		if replayErr == nil {
			replayErr = fmt.Errorf("replayed %d of %d commits", len(replayed), len(prototypeCommits))
		}
		return PrototypeImportResult{}, fmt.Errorf("validate prototype target: %w", replayErr)
	}
	if err := os.Rename(tmp, targetDir); err != nil {
		return PrototypeImportResult{}, fmt.Errorf("publish prototype target: %w", err)
	}
	published = true
	result.ImportedEvents = lastSequence
	return result, nil
}

func convertPrototypeCommits(prototypeCommits []Commit, sourceID, targetID, sourceCodec string) ([]byte, uint64, error) {
	finalCommits := make([]Commit, len(prototypeCommits))
	var lastSequence uint64
	for i, original := range prototypeCommits {
		commit := cloneCommit(original)
		commit.Codec = Codec
		commit.ID = deterministicID("prototype-commit\x00" + targetID + "\x00" + original.ID)
		commit.OperationID = "prototype:" + sourceID + ":" + original.ID
		commit.WriterGeneration = 1
		for eventIndex := range commit.Events {
			if sourceCodec == PrototypeCodec && commit.Events[eventIndex].Kind == "context/replace" {
				commit.Events[eventIndex].Kind = "history/replace"
			}
		}
		operationHash, hashErr := hashOperation(targetID, commit.TurnID, commit.Events)
		if hashErr != nil {
			return nil, 0, hashErr
		}
		commit.OperationHash = operationHash
		finalCommits[i] = commit
		lastSequence = commit.LastSequence()
	}
	if _, err := Project(finalCommits); err != nil {
		return nil, 0, fmt.Errorf("validate imported prototype projection: %w", err)
	}
	var finalLog bytes.Buffer
	for _, commit := range finalCommits {
		line, marshalErr := json.Marshal(commit)
		if marshalErr != nil {
			return nil, 0, marshalErr
		}
		finalLog.Write(line)
		finalLog.WriteByte('\n')
	}
	return finalLog.Bytes(), lastSequence, nil
}
