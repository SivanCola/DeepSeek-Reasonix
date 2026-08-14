package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"reasonix/internal/fileutil"
	fileenc "reasonix/internal/fileutil/encoding"
)

func (s *Store) turnsDir() string {
	return filepath.Join(s.dir, "turns")
}

func (s *Store) turnDir(turn int) string {
	return filepath.Join(s.turnsDir(), strconv.Itoa(turn))
}

func (s *Store) v3MetaPath(turn int) string {
	return filepath.Join(s.turnDir(turn), "meta.json")
}

func (s *Store) v3BeforePath(turn, index int) string {
	return filepath.Join(s.turnDir(turn), "files", fmt.Sprintf("%04d.before", index))
}

func v3PayloadBytes(f FileSnap) []byte {
	if f.Content == nil {
		return nil
	}
	if f.Encoding != nil {
		return fileenc.Encode(*f.Content, *f.Encoding)
	}
	return []byte(*f.Content)
}

func (s *Store) persistV3(c *Checkpoint) error {
	turnDir := s.turnDir(c.Turn)
	if err := os.MkdirAll(filepath.Join(turnDir, "files"), 0o755); err != nil {
		return err
	}
	wire := *c
	wire.SchemaVersion = SchemaV3
	wire.Files = make([]FileSnap, len(c.Files))
	for i, f := range c.Files {
		snap := f
		snap.BlobRef = ""
		payloadPath := s.v3BeforePath(c.Turn, i)
		if f.Content != nil && !f.PayloadExpired {
			if err := fileutil.AtomicWriteFile(payloadPath, v3PayloadBytes(f), 0o644); err != nil {
				return err
			}
			snap.Content = nil
			snap.Encoding = nil
		} else if err := os.Remove(payloadPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		wire.Files[i] = snap
	}
	b, err := json.Marshal(&wire)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(s.v3MetaPath(c.Turn), b, 0o644)
}

func (s *Store) removeTurnArtifacts(turns map[int]bool) error {
	if s.dir == "" {
		return nil
	}
	for turn := range turns {
		for _, path := range []string{
			filepath.Join(s.dir, fmt.Sprintf("turn-%d.json", turn)),
			filepath.Join(s.expiredDir(), fmt.Sprintf("turn-%d.json", turn)),
		} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove checkpoint turn %d: %w", turn, err)
			}
		}
		if err := os.RemoveAll(s.turnDir(turn)); err != nil {
			return fmt.Errorf("remove checkpoint turn %d: %w", turn, err)
		}
	}
	return nil
}

func (s *Store) loadV3Turns(seen map[int]bool) {
	ents, err := os.ReadDir(s.turnsDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		turn, err := strconv.Atoi(e.Name())
		if err != nil || seen[turn] {
			continue
		}
		b, err := fileenc.ReadFileUTF8(s.v3MetaPath(turn))
		if err != nil {
			continue
		}
		var c Checkpoint
		if json.Unmarshal(b, &c) != nil {
			continue
		}
		c.Turn = turn
		c.SchemaVersion = SchemaV3
		for i := range c.Files {
			if c.Files[i].PayloadExpired {
				continue
			}
			raw, err := os.ReadFile(s.v3BeforePath(turn, i))
			if err != nil {
				continue
			}
			if want := c.Files[i].SHA256; want != "" && Digest(raw) != want {
				continue
			}
			enc, detected := fileenc.Detect(raw)
			text := string(fileenc.Decode(detected, enc))
			c.Files[i].Content = &text
			c.Files[i].Encoding = &enc
			c.Files[i].BlobRef = ""
		}
		seen[turn] = true
		s.done = append(s.done, &c)
	}
}

// pruneV3TurnsLocked applies retention to complete v3 turn directories. Older
// v1/v2 metadata remains on the legacy payload-expiration path so upgrades keep
// reading and cleaning data written by previous releases.
func (s *Store) pruneV3TurnsLocked() {
	if s.dir == "" || s.retainN <= 0 {
		return
	}
	var turns []*Checkpoint
	for _, c := range s.all() {
		if c.SchemaVersion >= SchemaV3 {
			turns = append(turns, c)
		}
	}
	excess := len(turns) - s.retainN
	if excess <= 0 {
		return
	}
	removed := make(map[*Checkpoint]bool, excess)
	for _, c := range turns {
		if excess == 0 {
			break
		}
		if c == s.cur || s.protectTurns[c.Turn] {
			continue
		}
		if err := os.RemoveAll(s.turnDir(c.Turn)); err != nil {
			continue
		}
		removed[c] = true
		excess--
	}
	if len(removed) == 0 {
		return
	}
	kept := s.done[:0]
	for _, c := range s.done {
		if !removed[c] {
			kept = append(kept, c)
		}
	}
	s.done = kept
	// Pre-v3 builds briefly wrote both a turn directory and a blob. Once such a
	// turn ages out, the legacy mark-and-sweep can reclaim its orphaned blob.
	s.pruneBlobsLocked()
}
