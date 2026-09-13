package sessionv3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/fileutil"
)

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
	if err := ctx.Err(); err != nil {
		return PrototypeImportResult{}, err
	}
	sourceDir = filepath.Clean(strings.TrimSpace(sourceDir))
	targetRoot = filepath.Clean(strings.TrimSpace(targetRoot))
	if sourceDir == "." || targetRoot == "." {
		return PrototypeImportResult{}, fmt.Errorf("sessionv3: prototype source and target root are required")
	}
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	eventsPath := filepath.Join(sourceDir, "events.jsonl")
	lease, err := agent.TryAcquireSessionLease(eventsPath)
	if err != nil {
		return PrototypeImportResult{}, fmt.Errorf("freeze prototype session: %w", err)
	}
	defer lease.Release()

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return PrototypeImportResult{}, err
	}
	var prototype Manifest
	if err := json.Unmarshal(manifestBytes, &prototype); err != nil {
		return PrototypeImportResult{}, fmt.Errorf("%w: prototype manifest: %w", ErrDamagedStore, err)
	}
	if prototype.SchemaVersion != SchemaVersion || prototype.Codec != PrototypeCodec || strings.TrimSpace(prototype.SessionID) == "" {
		return PrototypeImportResult{}, fmt.Errorf("%w: expected %s", ErrUnsupportedVersion, PrototypeCodec)
	}
	eventBytes, err := os.ReadFile(eventsPath)
	if os.IsNotExist(err) {
		eventBytes = nil
	} else if err != nil {
		return PrototypeImportResult{}, err
	}
	digest := sha256.New()
	digest.Write(manifestBytes)
	digest.Write([]byte{0})
	digest.Write(eventBytes)
	sourceDigest := hex.EncodeToString(digest.Sum(nil))
	source := Source{Path: sourceDir, Size: int64(len(manifestBytes) + len(eventBytes)), SHA256: sourceDigest, Version: PrototypeCodec}
	targetID := deterministicID("prototype-import\x00" + sourceDir + "\x00" + sourceDigest)
	targetDir := filepath.Join(targetRoot, targetID)
	result := PrototypeImportResult{TargetID: targetID, TargetDir: targetDir, Source: source}
	if existing, readErr := readManifest(filepath.Join(targetDir, "manifest.json")); readErr == nil {
		if existing.Source != nil && existing.Source.Path == source.Path && existing.Source.SHA256 == source.SHA256 && existing.Source.Version == PrototypeCodec {
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

	frozen, err := os.Open(filepath.Join(legacyDir, "events.jsonl"))
	if err != nil {
		return PrototypeImportResult{}, err
	}
	prototypeCommits := []Commit{}
	err = scanCommitFileCodec(frozen, 0, 1, PrototypeCodec, PrototypeProjectionKinds, func(_ int64, commit Commit) bool {
		prototypeCommits = append(prototypeCommits, cloneCommit(commit))
		return true
	})
	_ = frozen.Close()
	if err != nil {
		return PrototypeImportResult{}, err
	}

	finalLog, lastSequence, err := convertPrototypeCommits(prototypeCommits, prototype.SessionID, targetID)
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

func convertPrototypeCommits(prototypeCommits []Commit, sourceID, targetID string) ([]byte, uint64, error) {
	finalCommits := make([]Commit, len(prototypeCommits))
	var lastSequence uint64
	for i, original := range prototypeCommits {
		commit := cloneCommit(original)
		commit.Codec = Codec
		commit.ID = deterministicID("prototype-commit\x00" + targetID + "\x00" + original.ID)
		commit.OperationID = "prototype:" + sourceID + ":" + original.ID
		commit.WriterGeneration = 1
		for eventIndex := range commit.Events {
			if commit.Events[eventIndex].Kind == "context/replace" {
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
