package sessionv3

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/filelock"
)

// SetTitle updates mutable session metadata through the canonical event log.
// A cold write acquires the ordinary writer lease, flushes the event, and then
// releases the exact Runtime; no title sidecar becomes a second source of truth.
func (s *Service) SetTitle(ctx context.Context, ref SessionRef, title string) error {
	if err := ref.validate(s.hostID); err != nil {
		return err
	}
	runtime, alreadyOpen := s.Runtime(ref)
	var session *Session
	var cold SessionHandle
	var err error
	if alreadyOpen {
		session = runtime.Session()
	} else {
		cold, err = s.persistence.Open(ref.SessionID, ReadWrite)
		if err != nil {
			return err
		}
		defer cold.Close(context.Background())
		writable, ok := cold.(WritableSessionHandle)
		if !ok {
			return errors.New("sessionv3: persistence returned a non-writable title handle")
		}
		session = &Session{Handle: writable}
	}
	payload, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	if _, err = session.AppendBatch(ctx, fmt.Sprintf("session-title:%s:%x", ref.SessionID, digest), []Event{{Kind: "session/title", Payload: payload}}); err != nil {
		return err
	}
	_, err = session.Flush(ctx)
	return err
}

// Export writes a self-contained immutable copy of the session directory. It
// first establishes a durability checkpoint, then holds the Store commit and
// drain boundaries while copying, so the exported manifest and event prefix
// cannot describe different moments.
func (s *Store) Export(ctx context.Context, destination string) error {
	if s == nil {
		return os.ErrClosed
	}
	if _, err := s.Flush(ctx); err != nil {
		return err
	}
	s.drainMu.Lock()
	s.mu.Lock()
	if s.closed || s.file == nil {
		s.mu.Unlock()
		s.drainMu.Unlock()
		return os.ErrClosed
	}
	if len(s.pending) != 0 || s.draining {
		s.mu.Unlock()
		s.drainMu.Unlock()
		return fmt.Errorf("sessionv3: export raced a new accepted batch")
	}
	source := s.dir
	err := exportDirectory(ctx, source, destination)
	s.mu.Unlock()
	s.drainMu.Unlock()
	return err
}

func (p *FilesystemPersistence) exportCold(ctx context.Context, sessionID, destination string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	source := filepath.Join(p.Root, sessionID)
	if _, err := readManifest(filepath.Join(source, "manifest.json")); err != nil {
		return err
	}
	release, err := filelock.AcquireMode(ctx, filepath.Join(source, "writer.lock"), filelock.ModeShared)
	if err != nil {
		return fmt.Errorf("sessionv3: freeze cold export: %w", err)
	}
	defer release()
	return exportDirectory(ctx, source, destination)
}

func exportDirectory(ctx context.Context, source, destination string) error {
	source = filepath.Clean(source)
	destination = filepath.Clean(strings.TrimSpace(destination))
	if destination == "." || destination == source || strings.HasPrefix(destination+string(os.PathSeparator), source+string(os.PathSeparator)) {
		return fmt.Errorf("sessionv3: invalid export destination %q", destination)
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("%w: export destination", ErrSessionExists)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".session-export-")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tmp)
		}
	}()
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		if relative == "writer.lock" || relative == "events.offset-index.json" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(tmp, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sessionv3: export refuses symlink %s", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("sessionv3: export refuses non-regular file %s", relative)
		}
		return copySessionFile(ctx, path, target, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	manifest, err := readManifest(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		return fmt.Errorf("validate export manifest: %w", err)
	}
	if _, err := Replay(tmp, nil); err != nil {
		return fmt.Errorf("validate export events: %w", err)
	}
	if manifest.SessionID == "" {
		return fmt.Errorf("%w: export has empty session id", ErrDamagedStore)
	}
	if err := os.Rename(tmp, destination); err != nil {
		return err
	}
	published = true
	return nil
}

func copySessionFile(ctx context.Context, source, target string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, &contextReader{ctx: ctx, reader: in})
	syncErr := out.Sync()
	closeErr := out.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

// Delete removes a cold session from the visible root while holding its
// cross-process writer lock. Rename makes the catalog change atomic; cleanup of
// the private tombstone may then fail visibly without reviving the session.
func (p *FilesystemPersistence) Delete(ctx context.Context, sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	source := filepath.Join(p.Root, sessionID)
	if _, err := readManifest(filepath.Join(source, "manifest.json")); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
		}
		return err
	}
	release, err := filelock.Acquire(ctx, filepath.Join(source, "writer.lock"))
	if err != nil {
		return fmt.Errorf("sessionv3: delete ownership: %w", err)
	}
	trashRoot := filepath.Join(p.Root, ".trash")
	if err := os.MkdirAll(trashRoot, 0o700); err != nil {
		release()
		return err
	}
	tombstone := filepath.Join(trashRoot, sessionID+"-"+randomID())
	if err := os.Rename(source, tombstone); err != nil {
		release()
		return err
	}
	release()
	_ = os.RemoveAll(filepath.Join(p.Root, ".query-cache", sessionID))
	return os.RemoveAll(tombstone)
}

func (s *Service) Export(ctx context.Context, ref SessionRef, destination string) error {
	if err := ref.validate(s.hostID); err != nil {
		return err
	}
	if runtime, ok := s.Runtime(ref); ok {
		return runtime.session.Handle.Export(ctx, destination)
	}
	filesystem, ok := s.persistence.(*FilesystemPersistence)
	if !ok {
		return errors.New("sessionv3: persistence does not support export")
	}
	return filesystem.exportCold(ctx, ref.SessionID, destination)
}

func (s *Service) Delete(ctx context.Context, ref SessionRef) error {
	if err := ref.validate(s.hostID); err != nil {
		return err
	}
	if runtime, ok := s.Runtime(ref); ok {
		if err := s.Close(ctx, ref); err != nil {
			return err
		}
		// Exact-instance removal above completed before filesystem deletion;
		// delayed callbacks cannot remove a successor runtime.
		_ = runtime
	}
	filesystem, ok := s.persistence.(*FilesystemPersistence)
	if !ok {
		return errors.New("sessionv3: persistence does not support delete")
	}
	return filesystem.Delete(ctx, ref.SessionID)
}
