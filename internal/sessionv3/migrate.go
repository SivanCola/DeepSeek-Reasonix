package sessionv3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/filelock"
	"reasonix/internal/fileutil"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

type MigrationMapping struct {
	SchemaVersion int              `json:"schemaVersion"`
	Entries       []MigrationEntry `json:"entries"`
}

type MigrationEntry struct {
	SourcePath   string    `json:"sourcePath"`
	SourceSize   int64     `json:"sourceSize"`
	SourceSHA256 string    `json:"sourceSha256"`
	LegacyHeadID string    `json:"legacyHeadId,omitempty"`
	TargetCodec  string    `json:"targetCodec"`
	TargetID     string    `json:"targetId"`
	CreatedAt    time.Time `json:"createdAt"`
}

type MigrationResult struct {
	TargetID   string
	TargetDir  string
	Source     Source
	Reused     bool
	MessageNum int
}

var migrationMu sync.Mutex

// MigrateLegacy freezes one legacy session under its write lease, constructs a
// complete v3 directory in a sibling temporary directory, then publishes it by
// rename. Original artifacts are copied byte-for-byte under legacy/ and are
// never rewritten or removed.
func MigrateLegacy(ctx context.Context, sourcePath, targetRoot string) (MigrationResult, error) {
	return MigrateLegacyHead(ctx, sourcePath, targetRoot, "")
}

// MigrateLegacyHead turns one reachable legacy DAG head into its own linear v3
// session. An empty head ID imports the legacy file's selected/default view.
// Head identity participates in both the deterministic target ID and migration
// map key, so continuing two old heads can never merge their future writes.
func MigrateLegacyHead(ctx context.Context, sourcePath, targetRoot, legacyHeadID string) (MigrationResult, error) {
	return migrateLegacyHead(ctx, sourcePath, targetRoot, legacyHeadID, false)
}

// migrateLegacyHeadForHost permits an existing host transition to freeze a
// legacy source already leased by this process. Cross-process ownership is
// still enforced by the OS lease. Callers must serialize the transition with
// their host/session gate and stop the old producer before invoking it.
func migrateLegacyHeadForHost(ctx context.Context, sourcePath, targetRoot, legacyHeadID string) (MigrationResult, error) {
	return migrateLegacyHead(ctx, sourcePath, targetRoot, legacyHeadID, true)
}

func migrateLegacyHead(ctx context.Context, sourcePath, targetRoot, legacyHeadID string, allowCurrentOwner bool) (MigrationResult, error) {
	if err := ctx.Err(); err != nil {
		return MigrationResult{}, err
	}
	sourcePath = agent.CanonicalSessionPath(sourcePath)
	targetRoot = filepath.Clean(strings.TrimSpace(targetRoot))
	legacyHeadID = strings.TrimSpace(legacyHeadID)
	if sourcePath == "" || targetRoot == "." {
		return MigrationResult{}, fmt.Errorf("sessionv3: source and target root are required")
	}
	var lease *agent.SessionLease
	if !allowCurrentOwner || !agent.SessionLeaseHeldByCurrentRuntime(sourcePath) {
		var acquireErr error
		lease, acquireErr = agent.TryAcquireSessionLease(sourcePath)
		if acquireErr != nil {
			return MigrationResult{}, fmt.Errorf("freeze legacy session: %w", acquireErr)
		}
		defer lease.Release()
	}
	if err := rejectUnresolvedLegacySidecar(sourcePath); err != nil {
		return MigrationResult{}, err
	}

	artifacts, source, err := freezeLegacyArtifacts(ctx, sourcePath)
	if err != nil {
		return MigrationResult{}, err
	}
	source.LegacyHeadID = legacyHeadID
	targetID := migrationTargetID(sourcePath, source.SHA256, legacyHeadID)
	targetDir := filepath.Join(targetRoot, targetID)
	result := MigrationResult{TargetID: targetID, TargetDir: targetDir, Source: source}

	migrationMu.Lock()
	defer migrationMu.Unlock()
	if m, err := readManifest(filepath.Join(targetDir, "manifest.json")); err == nil {
		if m.Source != nil && m.Source.Path == sourcePath && m.Source.SHA256 == source.SHA256 && m.Source.LegacyHeadID == legacyHeadID {
			if err := appendMigrationMapping(ctx, targetRoot, MigrationEntry{SourcePath: sourcePath, SourceSize: source.Size, SourceSHA256: source.SHA256, LegacyHeadID: legacyHeadID, TargetCodec: Codec, TargetID: targetID, CreatedAt: m.CreatedAt}); err != nil {
				return MigrationResult{}, fmt.Errorf("repair migration mapping: %w", err)
			}
			result.Reused = true
			return result, nil
		}
		return MigrationResult{}, fmt.Errorf("sessionv3: target %s already exists for different input", targetID)
	} else if !os.IsNotExist(err) {
		return MigrationResult{}, err
	}
	if err := os.MkdirAll(targetRoot, 0o700); err != nil {
		return MigrationResult{}, err
	}
	tmp, err := os.MkdirTemp(targetRoot, "."+targetID+".tmp-")
	if err != nil {
		return MigrationResult{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tmp)
		}
	}()

	manifest := Manifest{SchemaVersion: SchemaVersion, Codec: Codec, SessionID: targetID, CreatedAt: time.Now().UTC(), Source: &source}
	if err := writeManifest(filepath.Join(tmp, "manifest.json"), manifest); err != nil {
		return MigrationResult{}, err
	}
	legacyDir := filepath.Join(tmp, "legacy")
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return MigrationResult{}, err
		}
		rel := filepath.Base(artifact.path)
		if err := copyFrozenArtifact(ctx, artifact.data, filepath.Join(legacyDir, rel), artifact.mode); err != nil {
			return MigrationResult{}, err
		}
	}

	// Parse only the frozen copy. The source lease prevents cooperating writers,
	// but reading the original again would still make the published target depend
	// on bytes outside the frozen migration input.
	frozenSourcePath := filepath.Join(legacyDir, filepath.Base(sourcePath))
	var session *agent.Session
	if legacyHeadID == "" {
		session, err = agent.LoadSession(frozenSourcePath)
	} else {
		session, err = agent.LoadSessionHeadReadOnly(frozenSourcePath, legacyHeadID)
	}
	if err != nil {
		return MigrationResult{}, fmt.Errorf("read legacy transcript: %w", err)
	}
	messages := session.Snapshot()
	if messages == nil {
		// An empty legacy transcript is valid. Encode an explicit empty list so
		// strict replay can distinguish it from a damaged import missing the
		// required messages field.
		messages = []provider.Message{}
	}
	result.MessageNum = len(messages)
	payload := map[string]any{
		"source":   source,
		"messages": messages,
	}
	if modelRef, modelIdentity, ok := agent.LoadSessionModelSelection(frozenSourcePath); ok && strings.TrimSpace(modelRef) != "" {
		payload["modelRef"] = strings.TrimSpace(modelRef)
		payload["modelIdentity"] = strings.TrimSpace(modelIdentity)
	}
	if goal := sanitizedLegacyGoal(frozenSourcePath); goal != nil {
		payload["goal"] = goal
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return MigrationResult{}, err
	}
	v3, err := Open(tmp, targetID)
	if err != nil {
		return MigrationResult{}, err
	}
	_, appendErr := v3.Append(ctx, Batch{OperationID: "legacy-import:" + source.SHA256, Events: []Event{{Kind: "legacy/import", Payload: raw}}})
	if appendErr == nil {
		_, appendErr = v3.Flush(ctx)
	}
	closeErr := v3.Close(ctx)
	if appendErr != nil {
		return MigrationResult{}, appendErr
	}
	if closeErr != nil {
		return MigrationResult{}, closeErr
	}
	if err := os.Rename(tmp, targetDir); err != nil {
		return MigrationResult{}, fmt.Errorf("publish v3 session: %w", err)
	}
	published = true
	if err := appendMigrationMapping(ctx, targetRoot, MigrationEntry{SourcePath: sourcePath, SourceSize: source.Size, SourceSHA256: source.SHA256, LegacyHeadID: legacyHeadID, TargetCodec: Codec, TargetID: targetID, CreatedAt: time.Now().UTC()}); err != nil {
		// The target is already complete and deterministically discoverable. A
		// later retry repairs the mapping without rebuilding or resending work.
		return result, fmt.Errorf("publish migration mapping: %w", err)
	}
	return result, nil
}

type frozenArtifact struct {
	path string
	mode fs.FileMode
	data []byte
}

func freezeLegacyArtifacts(ctx context.Context, sourcePath string) ([]frozenArtifact, Source, error) {
	paths := append([]string{sourcePath}, store.SessionSidecarFiles(sourcePath)...)
	seen := map[string]bool{}
	artifacts := []frozenArtifact{}
	foundSource := false
	hash := sha256.New()
	var total int64
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, Source{}, err
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, Source{}, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, Source{}, err
		}
		artifacts = append(artifacts, frozenArtifact{path: path, mode: info.Mode().Perm(), data: data})
		if path == sourcePath {
			foundSource = true
		}
	}
	if !foundSource {
		return nil, Source{}, os.ErrNotExist
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].path < artifacts[j].path })
	for _, artifact := range artifacts {
		name := filepath.Base(artifact.path)
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write(artifact.data)
		hash.Write([]byte{0})
		total += int64(len(artifact.data))
	}
	return artifacts, Source{Path: sourcePath, Size: total, SHA256: hex.EncodeToString(hash.Sum(nil)), Version: "legacy"}, nil
}

func copyFrozenArtifact(ctx context.Context, data []byte, target string, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(target, data, mode)
}

func sanitizedLegacyGoal(sourcePath string) map[string]any {
	b, err := os.ReadFile(store.SessionGoalState(sourcePath))
	if err != nil {
		return nil
	}
	var goal map[string]any
	if json.Unmarshal(b, &goal) != nil {
		return nil
	}
	delete(goal, "todos")
	delete(goal, "todo")
	delete(goal, "auto_continue")
	delete(goal, "autoContinue")
	return goal
}

func migrationTargetID(path, digest, legacyHeadID string) string {
	sum := sha256.Sum256([]byte(path + "\x00" + digest + "\x00" + legacyHeadID + "\x00" + Codec))
	return hex.EncodeToString(sum[:12])
}

func readManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, err
	}
	if m.SchemaVersion != SchemaVersion || m.Codec != Codec {
		return Manifest{}, fmt.Errorf("%w: manifest schema or codec", ErrUnsupportedVersion)
	}
	return m, nil
}

func writeManifest(path string, m Manifest) error {
	if m.Codec == "" {
		m.Codec = Codec
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(path, append(b, '\n'), 0o600)
}

func appendMigrationMapping(ctx context.Context, root string, entry MigrationEntry) error {
	path := filepath.Join(root, "migration-map.json")
	release, err := acquireMigrationMapLease(ctx, path)
	if err != nil {
		return fmt.Errorf("lock migration map: %w", err)
	}
	defer release()
	mapping := MigrationMapping{SchemaVersion: SchemaVersion, Entries: []MigrationEntry{}}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &mapping); err != nil {
			return err
		}
		if mapping.SchemaVersion != SchemaVersion {
			return fmt.Errorf("%w: migration map schema %d", ErrUnsupportedVersion, mapping.SchemaVersion)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, existing := range mapping.Entries {
		if existing.SourcePath == entry.SourcePath && existing.SourceSHA256 == entry.SourceSHA256 && existing.LegacyHeadID == entry.LegacyHeadID && existing.TargetCodec == entry.TargetCodec {
			return nil
		}
	}
	mapping.Entries = append(mapping.Entries, entry)
	sort.Slice(mapping.Entries, func(i, j int) bool {
		if mapping.Entries[i].SourcePath == mapping.Entries[j].SourcePath {
			if mapping.Entries[i].SourceSHA256 == mapping.Entries[j].SourceSHA256 {
				if mapping.Entries[i].LegacyHeadID == mapping.Entries[j].LegacyHeadID {
					return mapping.Entries[i].TargetCodec < mapping.Entries[j].TargetCodec
				}
				return mapping.Entries[i].LegacyHeadID < mapping.Entries[j].LegacyHeadID
			}
			return mapping.Entries[i].SourceSHA256 < mapping.Entries[j].SourceSHA256
		}
		return mapping.Entries[i].SourcePath < mapping.Entries[j].SourcePath
	})
	b, err := json.MarshalIndent(mapping, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(path, append(b, '\n'), 0o600)
}

func acquireMigrationMapLease(ctx context.Context, path string) (func(), error) {
	return filelock.Acquire(ctx, path+".lock")
}

func IsUnsupported(err error) bool { return errors.Is(err, ErrUnsupportedVersion) }
