package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SaveOptions struct {
	ExpectedRevision        int
	RequireExpectedRevision bool
	RequireCreate           bool
}

type SaveResult struct {
	Path     string
	Memory   Memory
	Previous *Memory
}

type MigrationReport struct {
	Migrated int
}

var memoryStoreMutationMu sync.Mutex

func (s Store) MigrateV2() (MigrationReport, error) {
	memoryStoreMutationMu.Lock()
	defer memoryStoreMutationMu.Unlock()
	var report MigrationReport
	for _, dir := range s.dirs() {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return report, err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Name() == indexFile || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				return report, err
			}
			frontmatter, _ := splitFrontmatter(string(raw))
			if strings.TrimSpace(frontmatter["id"]) != "" && parsePositiveInt(frontmatter["revision"]) > 0 {
				continue
			}
			memory, ok := loadMemory(path)
			if !ok {
				continue
			}
			memory.Name = slug(memory.Name)
			if memory.Scope == "" {
				memory.Scope = s.scopeForDir(dir)
			}
			if err := writeMemoryAtomic(path, []byte(render(memory, memory.Name)), 0o644); err != nil {
				return report, err
			}
			if err := reindexIn(dir, memory.Name, memory); err != nil {
				return report, err
			}
			report.Migrated++
		}
	}
	return report, nil
}

func (s Store) SaveWithOptions(m Memory, opts SaveOptions) (SaveResult, error) {
	memoryStoreMutationMu.Lock()
	defer memoryStoreMutationMu.Unlock()

	var existing Memory
	var existingPath string
	var exists bool
	if strings.TrimSpace(m.ID) != "" {
		existing, existingPath, exists = s.findActive(m.ID)
		if !exists {
			return SaveResult{}, fmt.Errorf("memory id %q not found", m.ID)
		}
	} else if strings.TrimSpace(m.Name) != "" {
		existing, existingPath, exists = s.findActive(m.Name)
	}
	if opts.RequireExpectedRevision {
		actual := 0
		if exists {
			actual = existing.Revision
		}
		if actual != opts.ExpectedRevision {
			return SaveResult{}, fmt.Errorf("memory revision conflict: expected %d, found %d", opts.ExpectedRevision, actual)
		}
	}
	if opts.RequireCreate && exists {
		return SaveResult{}, fmt.Errorf("memory %q already exists; automatic writes are create-only", existing.Name)
	}

	if strings.TrimSpace(m.Name) == "" {
		if !exists {
			return SaveResult{}, fmt.Errorf("memory needs a name")
		}
		m.Name = existing.Name
	}
	m.Name = slug(m.Name)
	if m.Name == "" {
		return SaveResult{}, fmt.Errorf("memory name needs at least one letter or digit")
	}
	if collision, _, ok := s.findActive(m.Name); ok && (!exists || collision.ID != existing.ID) {
		return SaveResult{}, fmt.Errorf("memory name %q is already used by id %q", m.Name, collision.ID)
	}

	now := time.Now().UTC()
	if exists {
		m.ID = existing.ID
		m.Revision = existing.Revision + 1
		m.CreatedAt = existing.CreatedAt
		if strings.TrimSpace(string(m.Scope)) == "" {
			m.Scope = existing.Scope
		}
	} else {
		m.ID = newMemoryID(m.Name, now)
		m.Revision = 1
		m.CreatedAt = now
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	m.Type = NormalizeType(string(m.Type))
	if strings.TrimSpace(string(m.Scope)) == "" {
		m.Scope = FactScopeProject
	} else {
		m.Scope = NormalizeFactScope(string(m.Scope))
	}

	dir := s.DirFor(m.Scope)
	if dir == "" {
		return SaveResult{}, fmt.Errorf("memory store unavailable (no user config dir)")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SaveResult{}, err
	}
	path, err := safeJoin(dir, m.Name+".md")
	if err != nil {
		return SaveResult{}, err
	}
	if exists {
		if err := snapshotMemoryRevision(existingPath, existing); err != nil {
			return SaveResult{}, err
		}
	}
	if err := writeMemoryAtomic(path, []byte(render(m, m.Name)), 0o644); err != nil {
		return SaveResult{}, err
	}
	if exists && cleanMemoryPath(existingPath) != cleanMemoryPath(path) {
		if err := os.Remove(existingPath); err != nil && !os.IsNotExist(err) {
			return SaveResult{}, err
		}
		oldDir := filepath.Dir(existingPath)
		if err := flushIndexIn(oldDir, indexLinesExceptIn(oldDir, existing.Name)); err != nil {
			return SaveResult{}, err
		}
	}
	if err := reindexIn(dir, m.Name, m); err != nil {
		return SaveResult{Path: path, Memory: m}, err
	}
	for _, otherDir := range s.dirs() {
		if sameDir(otherDir, dir) {
			continue
		}
		if duplicate, duplicatePath, ok := s.findActiveInDir(otherDir, m.Name); ok && duplicate.ID != m.ID {
			if _, err := archiveInDir(otherDir, duplicate.Name); err != nil {
				return SaveResult{}, err
			}
			if err := flushIndexIn(otherDir, indexLinesExceptIn(otherDir, duplicate.Name)); err != nil {
				return SaveResult{}, err
			}
			_ = duplicatePath
		}
	}

	result := SaveResult{Path: path, Memory: m}
	if exists {
		previous := existing
		result.Previous = &previous
	}
	return result, nil
}

func (s Store) Read(ref string) (Memory, bool) {
	memory, _, ok := s.findActive(ref)
	return memory, ok
}

func (s Store) findActive(ref string) (Memory, string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Memory{}, "", false
	}
	for i := len(s.dirs()) - 1; i >= 0; i-- {
		dir := s.dirs()[i]
		if memory, path, ok := s.findActiveInDir(dir, ref); ok {
			return memory, path, true
		}
	}
	return Memory{}, "", false
}

func (s Store) findActiveInDir(dir, ref string) (Memory, string, bool) {
	if strings.TrimSpace(dir) == "" {
		return Memory{}, "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Memory{}, "", false
	}
	wantName := slug(ref)
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == indexFile || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		memory, ok := loadMemory(path)
		if !ok {
			continue
		}
		if memory.Scope == "" {
			memory.Scope = s.scopeForDir(dir)
		}
		if memory.ID == ref || slug(memory.Name) == wantName {
			memory.Name = slug(memory.Name)
			return memory, path, true
		}
	}
	return Memory{}, "", false
}

func (s Store) Revisions(ref string) []Memory {
	active, _, ok := s.findActive(ref)
	if !ok {
		return nil
	}
	seen := map[int]bool{}
	var revisions []Memory
	for _, dir := range s.dirs() {
		revisionDir := filepath.Join(dir, ".revisions", active.ID)
		entries, err := os.ReadDir(revisionDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			memory, ok := loadMemory(filepath.Join(revisionDir, entry.Name()))
			if !ok || memory.ID != active.ID || seen[memory.Revision] {
				continue
			}
			seen[memory.Revision] = true
			revisions = append(revisions, memory)
		}
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i].Revision > revisions[j].Revision })
	return revisions
}

func (s Store) Restore(ref string, revision int) (SaveResult, error) {
	active, ok := s.Read(ref)
	if !ok {
		return SaveResult{}, fmt.Errorf("memory %q not found", ref)
	}
	if revision == active.Revision {
		return SaveResult{Path: s.Path(active.Name), Memory: active}, nil
	}
	var target Memory
	found := false
	for _, candidate := range s.Revisions(active.ID) {
		if candidate.Revision == revision {
			target = candidate
			found = true
			break
		}
	}
	if !found {
		return SaveResult{}, fmt.Errorf("memory %q revision %d not found", active.ID, revision)
	}
	target.ID = active.ID
	return s.SaveWithOptions(target, SaveOptions{ExpectedRevision: active.Revision, RequireExpectedRevision: true})
}

func snapshotMemoryRevision(path string, memory Memory) error {
	if memory.ID == "" || memory.Revision < 1 {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dir := filepath.Join(filepath.Dir(path), ".revisions", memory.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%09d.md", memory.Revision)
	return writeMemoryAtomic(filepath.Join(dir, name), b, 0o644)
}

func writeMemoryAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".memory-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func newMemoryID(name string, now time.Time) string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err == nil {
		return "mem-" + hex.EncodeToString(raw)
	}
	sum := sha256.Sum256([]byte(name + "\x00" + strconv.FormatInt(now.UnixNano(), 10)))
	return "mem-" + hex.EncodeToString(sum[:16])
}

func legacyMemoryID(name string, scope FactScope) string {
	sum := sha256.Sum256([]byte("reasonix-memory-v2\x00" + string(NormalizeFactScope(string(scope))) + "\x00" + slug(name)))
	return "legacy-" + hex.EncodeToString(sum[:12])
}

func cleanMemoryPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
