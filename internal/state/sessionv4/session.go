package sessionv4

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/contract/provider"
)

// Manifest is a session directory's identity file.
type Manifest struct {
	SchemaVersion   int       `json:"schemaVersion"`
	Codec           string    `json:"codec"`
	StorageRevision int       `json:"storageRevision"`
	ContentRoot     string    `json:"contentRoot"`
	SessionID       string    `json:"sessionId"`
	CreatedAt       time.Time `json:"createdAt"`
}

func (m Manifest) Equal(other Manifest) bool {
	return m.SchemaVersion == other.SchemaVersion && m.Codec == other.Codec &&
		m.StorageRevision == other.StorageRevision && m.ContentRoot == other.ContentRoot &&
		m.SessionID == other.SessionID && m.CreatedAt.Equal(other.CreatedAt)
}

// Session is one conversation under a sessions-v4 root.
type Session struct {
	Dir      string
	Manifest Manifest
	// LogSize and LogModTime describe events.frames when the session was
	// opened; a later change means 1.x has written to it since.
	LogSize    int64
	LogModTime time.Time

	store fs.FS
	name  string
}

// Open reads a session directory's manifest. Revisions 0 to 3 share one frame
// format and differ only in which events may appear.
func Open(dir string) (Session, error) {
	s, err := OpenIn(dirFS(filepath.Dir(dir)), filepath.Base(dir))
	s.Dir = dir
	return s, err
}

// OpenIn reads the session named name directly under store, which confines
// every read it and the returned Session make to that tree.
func OpenIn(store fs.FS, name string) (Session, error) {
	raw, err := readManifest(store, path.Join(name, "manifest.json"))
	if err != nil {
		return Session{}, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Session{}, fmt.Errorf("%w: manifest: %w", ErrDamaged, err)
	}
	if m.SchemaVersion != schemaVersion || m.Codec != codec || m.StorageRevision < 0 || m.StorageRevision > 3 {
		return Session{}, fmt.Errorf("%w: schema %d, codec %q, revision %d", ErrUnsupported, m.SchemaVersion, m.Codec, m.StorageRevision)
	}
	if m.SessionID != name {
		return Session{}, fmt.Errorf("%w: manifest names session %q", ErrDamaged, m.SessionID)
	}
	s := Session{Dir: name, Manifest: m, store: store, name: name}
	if info, err := fs.Stat(store, s.logPath()); err == nil {
		s.LogSize, s.LogModTime = info.Size(), info.ModTime()
	}
	return s, nil
}

func (s Session) logPath() string { return path.Join(s.name, "events.frames") }

// UpdatedAt is when the conversation last changed: its log, or its creation.
func (s Session) UpdatedAt() time.Time {
	if s.LogModTime.After(s.Manifest.CreatedAt) {
		return s.LogModTime
	}
	return s.Manifest.CreatedAt
}

// Transcript is what the session holds now.
type Transcript struct {
	Messages []provider.Message
	Title    string
	ModelRef string
}

type messagesPayload struct {
	Messages []json.RawMessage `json:"messages"`
	Message  json.RawMessage   `json:"message"`
	IDs      []string          `json:"messageIds"`
	Title    string            `json:"title"`
	ModelRef string            `json:"modelRef"`
}

// Transcript folds the log's message events the way 1.x builds the
// conversation it shows: complete appends, upsert replaces by id or appends,
// retract removes, and a history replace or legacy import starts over.
func (s Session) Transcript() (Transcript, error) {
	return s.TranscriptContext(context.Background())
}

// TranscriptContext allows background readers to yield without publishing partial history.
func (s Session) TranscriptContext(ctx context.Context) (Transcript, error) {
	if err := ctx.Err(); err != nil {
		return Transcript{}, err
	}
	pool := poolFor(s.store, s.name, s.Manifest.ContentRoot)
	msgs := newMessageFold()
	var t Transcript
	if s.LogSize == 0 {
		return t, nil
	}
	err := scanCommitsContext(ctx, s.store, s.logPath(), pool, func(events []event) error {
		for _, ev := range events {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !messageKinds[ev.Kind] {
				continue
			}
			var body messagesPayload
			if err := json.Unmarshal(ev.resolved, &body); err != nil {
				return fmt.Errorf("%w: event %s: %w", ErrDamaged, ev.ID, err)
			}
			if err := msgs.apply(ev.Kind, body, pool); err != nil {
				return fmt.Errorf("%w: event %s: %w", ErrDamaged, ev.ID, err)
			}
			switch ev.Kind {
			case "session/title":
				t.Title = strings.TrimSpace(body.Title)
			case "session/config", "legacy/import":
				if ref := strings.TrimSpace(body.ModelRef); ref != "" {
					t.ModelRef = ref
				}
			}
		}
		return nil
	})
	if err != nil {
		return Transcript{}, err
	}
	t.Messages = make([]provider.Message, 0, len(msgs.slots))
	for _, slot := range msgs.slots {
		if slot != nil {
			t.Messages = append(t.Messages, slot.msg)
		}
	}
	return t, nil
}

var messageKinds = map[string]bool{
	"message/complete": true, "message/upsert": true, "message/retract": true,
	"history/replace": true, "legacy/import": true, "session/title": true, "session/config": true,
}

type messageFold struct {
	slots []*message
	ids   map[string]int
	empty []int
}

func newMessageFold() *messageFold { return &messageFold{ids: make(map[string]int)} }

func (f *messageFold) append(m message) {
	if m.id == "" {
		f.empty = append(f.empty, len(f.slots))
	} else {
		f.ids[m.id] = len(f.slots)
	}
	f.slots = append(f.slots, &m)
}

func (f *messageFold) apply(kind string, body messagesPayload, pool contentPool) error {
	switch kind {
	case "message/complete", "message/upsert":
		m, err := decodeMessage(body.Message, pool)
		if err != nil {
			return err
		}
		if i, ok := f.ids[m.id]; ok && m.id != "" {
			if kind == "message/upsert" {
				f.slots[i] = &m
			}
			return nil
		}
		f.append(m)
	case "message/retract":
		for _, id := range body.IDs {
			if id == "" {
				for _, i := range f.empty {
					f.slots[i] = nil
				}
				f.empty = nil
			} else if i, ok := f.ids[id]; ok {
				f.slots[i] = nil
				delete(f.ids, id)
			}
		}
	case "history/replace", "legacy/import":
		next := newMessageFold()
		for _, raw := range body.Messages {
			m, err := decodeMessage(raw, pool)
			if err != nil {
				return err
			}
			if _, found := next.ids[m.id]; m.id == "" || !found {
				next.append(m)
			}
		}
		*f = *next
	}
	return nil
}

// List returns the sessions directly under root. Entries 1.x keeps beside them
// — the content pool, caches, trash, locks, temporaries — start with a dot and
// are not sessions, and neither is a directory whose manifest does not read.
func List(root string) []Session {
	out := ListIn(dirFS(root))
	for i := range out {
		out[i].Dir = filepath.Join(root, out[i].name)
	}
	return out
}

// ListIn is List over a confined tree.
func ListIn(store fs.FS) []Session {
	entries, err := fs.ReadDir(store, ".")
	if err != nil {
		return nil
	}
	var out []Session
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if s, err := OpenIn(store, e.Name()); err == nil {
			out = append(out, s)
		}
	}
	return out
}

const maxManifestBytes = 1 << 20

func readManifest(store fs.FS, name string) ([]byte, error) {
	f, err := store.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxManifestBytes {
		return nil, fmt.Errorf("%w: manifest larger than %d bytes", ErrTooLarge, maxManifestBytes)
	}
	return raw, nil
}
