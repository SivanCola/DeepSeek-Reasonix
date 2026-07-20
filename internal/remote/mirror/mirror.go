// Package mirror stores local read-only copies of remote sessions for offline
// browsing and disaster recovery. The remote Controller is the sole writer of
// authority; mirrors never participate in normal session locks, index scans, or
// auto-recovery.
package mirror

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
	"reasonix/internal/remote/broker"
	"reasonix/internal/remote/protocol"
)

// Root returns <Reasonix home>/remote-mirrors.
func Root() string {
	base := config.MemoryUserDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "remote-mirrors")
}

// Store is a host+workspace scoped mirror tree.
type Store struct {
	Base string // root of remote-mirrors; empty uses Root()
}

func (s Store) root() string {
	if strings.TrimSpace(s.Base) != "" {
		return s.Base
	}
	return Root()
}

// WorkspaceDir returns the directory for one host fingerprint + workspace pair.
func (s Store) WorkspaceDir(fingerprint, workspace string) string {
	root := s.root()
	if root == "" {
		return ""
	}
	hostHash := broker.FingerprintHash(fingerprint)
	wsHash := broker.FingerprintHash(workspace)
	return filepath.Join(root, hostHash, wsHash)
}

// ManifestPath is the path of the workspace-level manifest.
func (s Store) ManifestPath(fingerprint, workspace string) string {
	dir := s.WorkspaceDir(fingerprint, workspace)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "manifest.json")
}

// SessionDir is where a single mirrored session's artifacts live.
func (s Store) SessionDir(fingerprint, workspace, sessionID string) string {
	dir := s.WorkspaceDir(fingerprint, workspace)
	if dir == "" {
		return ""
	}
	safe := config.BoundFilenameComponent(sessionID, 200)
	return filepath.Join(dir, "sessions", safe)
}

// WorkspaceManifest tracks mirrored sessions at the workspace level.
type WorkspaceManifest struct {
	HostFingerprint string                    `json:"hostFingerprint"`
	Workspace       string                    `json:"workspace"`
	Sessions        map[string]SessionMirror  `json:"sessions,omitempty"`
	UpdatedAt       time.Time                 `json:"updatedAt"`
}

// SessionMirror is the local index entry for one remote session.
type SessionMirror struct {
	SessionID string    `json:"sessionId"`
	Revision  int64     `json:"revision"`
	Digest    string    `json:"digest"`
	Label     string    `json:"label,omitempty"`
	ModelRef  string    `json:"modelRef,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	// OfflineOnly is true when the remote no longer has this session.
	OfflineOnly bool `json:"offlineOnly,omitempty"`
}

// LoadManifest reads the workspace manifest, returning an empty one when missing.
func (s Store) LoadManifest(fingerprint, workspace string) (WorkspaceManifest, error) {
	path := s.ManifestPath(fingerprint, workspace)
	m := WorkspaceManifest{
		HostFingerprint: fingerprint,
		Workspace:       workspace,
		Sessions:        map[string]SessionMirror{},
	}
	if path == "" {
		return m, fmt.Errorf("mirror path unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Sessions == nil {
		m.Sessions = map[string]SessionMirror{}
	}
	return m, nil
}

// SaveManifest writes the workspace manifest atomically.
func (s Store) SaveManifest(m WorkspaceManifest) error {
	path := s.ManifestPath(m.HostFingerprint, m.Workspace)
	if path == "" {
		return fmt.Errorf("mirror path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	m.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, data, 0o600)
}

// ApplyCheckpointResult classifies a checkpoint comparison.
type ApplyCheckpointResult string

const (
	// ResultSynced means revision+digest match; reconnect is normal.
	ResultSynced ApplyCheckpointResult = "synced"
	// ResultUpdated means the remote is newer; local mirror was replaced.
	ResultUpdated ApplyCheckpointResult = "updated"
	// ResultFork means local revision is higher or digests diverge; auto
	// overwrite is forbidden — restore must create a new remote session.
	ResultFork ApplyCheckpointResult = "fork"
	// ResultCreated means the session was not mirrored before and is now stored.
	ResultCreated ApplyCheckpointResult = "created"
)

// CompareCheckpoint decides what to do without mutating storage.
func CompareCheckpoint(local SessionMirror, remote protocol.CheckpointEvent) ApplyCheckpointResult {
	if local.SessionID == "" {
		return ResultCreated
	}
	if local.Revision == remote.Revision && local.Digest == remote.Digest {
		return ResultSynced
	}
	if remote.Revision > local.Revision {
		return ResultUpdated
	}
	if local.Revision > remote.Revision {
		return ResultFork
	}
	// Same revision, different digest → fork.
	if local.Digest != "" && remote.Digest != "" && local.Digest != remote.Digest {
		return ResultFork
	}
	return ResultUpdated
}

// ApplyCheckpoint writes artifacts under a temp directory then atomically
// replaces the session mirror. artifacts maps relative paths to contents.
// digest must match sha256 of the canonical artifact set (see DigestArtifacts).
func (s Store) ApplyCheckpoint(fingerprint, workspace string, manifest protocol.CheckpointManifest, artifacts map[string][]byte) (ApplyCheckpointResult, error) {
	if strings.TrimSpace(manifest.SessionID) == "" {
		return "", fmt.Errorf("checkpoint missing sessionId")
	}
	got := DigestArtifacts(artifacts)
	if manifest.Digest != "" && !strings.EqualFold(manifest.Digest, got) {
		return "", fmt.Errorf("checkpoint digest mismatch")
	}
	if manifest.Digest == "" {
		manifest.Digest = got
	}

	wm, err := s.LoadManifest(fingerprint, workspace)
	if err != nil {
		return "", err
	}
	local := wm.Sessions[manifest.SessionID]
	decision := CompareCheckpoint(local, protocol.CheckpointEvent{
		SessionID: manifest.SessionID,
		Revision:  manifest.Revision,
		Digest:    manifest.Digest,
	})
	if decision == ResultFork {
		return ResultFork, fmt.Errorf("mirror fork detected: local revision=%d digest=%s remote revision=%d digest=%s",
			local.Revision, local.Digest, manifest.Revision, manifest.Digest)
	}

	sessionDir := s.SessionDir(fingerprint, workspace, manifest.SessionID)
	if sessionDir == "" {
		return "", fmt.Errorf("mirror path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(sessionDir), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(sessionDir), ".mirror-"+manifest.SessionID+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	// Write manifest first.
	manBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, "checkpoint.json"), manBytes, 0o600); err != nil {
		return "", err
	}
	for rel, data := range artifacts {
		rel = filepath.Clean(rel)
		if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return "", fmt.Errorf("invalid artifact path %q", rel)
		}
		dest := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, data, 0o600); err != nil {
			return "", err
		}
	}

	// Atomic replace: rename tmp over sessionDir.
	backup := sessionDir + ".bak"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(sessionDir); err == nil {
		if err := os.Rename(sessionDir, backup); err != nil {
			return "", err
		}
	}
	if err := os.Rename(tmp, sessionDir); err != nil {
		_ = os.Rename(backup, sessionDir)
		return "", err
	}
	_ = os.RemoveAll(backup)

	wm.Sessions[manifest.SessionID] = SessionMirror{
		SessionID: manifest.SessionID,
		Revision:  manifest.Revision,
		Digest:    manifest.Digest,
		Label:     manifest.Label,
		ModelRef:  manifest.ModelRef,
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.SaveManifest(wm); err != nil {
		return decision, err
	}
	return decision, nil
}

// DigestArtifacts computes a stable digest over relative path → content pairs.
func DigestArtifacts(artifacts map[string][]byte) string {
	h := sha256.New()
	// Stable order.
	paths := make([]string, 0, len(artifacts))
	for p := range artifacts {
		paths = append(paths, p)
	}
	// Simple insertion sort to avoid importing sort for tiny maps in tests... use sort.
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if paths[j] < paths[i] {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
	}
	for _, p := range paths {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
		sum := sha256.Sum256(artifacts[p])
		_, _ = h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReadCheckpoint loads a stored checkpoint for offline view or restore.
func (s Store) ReadCheckpoint(fingerprint, workspace, sessionID string) (protocol.CheckpointManifest, map[string][]byte, error) {
	dir := s.SessionDir(fingerprint, workspace, sessionID)
	var man protocol.CheckpointManifest
	if dir == "" {
		return man, nil, fmt.Errorf("mirror path unavailable")
	}
	data, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		return man, nil, err
	}
	if err := json.Unmarshal(data, &man); err != nil {
		return man, nil, err
	}
	artifacts := map[string][]byte{}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "checkpoint.json" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		artifacts[filepath.ToSlash(rel)] = b
		return nil
	})
	return man, artifacts, nil
}

// MarkOffline marks a session as present only in the local mirror.
func (s Store) MarkOffline(fingerprint, workspace, sessionID string) error {
	wm, err := s.LoadManifest(fingerprint, workspace)
	if err != nil {
		return err
	}
	entry, ok := wm.Sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %q not mirrored", sessionID)
	}
	entry.OfflineOnly = true
	wm.Sessions[sessionID] = entry
	return s.SaveManifest(wm)
}
