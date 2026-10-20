package sessionstore

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"reasonix/internal/state/store"
)

// listIndexName is the remembered listing of one session folder. It lives in
// the folder, beside the sessions it describes, and is never a session itself.
const listIndexName = ".list-index.json"

// listIndexFormat changes whenever SessionOrderInfo or the way a row is read
// from the files changes, so an index written by another build is not trusted.
// TestListIndexFormatTracksTheRowShape holds it to the row's shape.
const listIndexFormat = 3

// listIndexMin is the folder size from which an index is kept. A small folder
// reads in milliseconds and is not worth a file of its own.
const listIndexMin = 100

// fileStamp is what one directory listing says about a file, with no open: a
// size and a modification time. A file that is absent has the zero stamp.
type fileStamp struct {
	Size int64 `json:"s,omitempty"`
	Mod  int64 `json:"m,omitempty"`
	Here bool  `json:"h,omitempty"`
}

// listPrint is every file a session's listing row is built from. The row is
// the same exactly while none of them changed, which is how a remembered row is
// told from a stale one without reading any of them.
type listPrint struct {
	Transcript fileStamp `json:"t"`
	Meta       fileStamp `json:"m"`
	Events     fileStamp `json:"e"`
}

type listIndexEntry struct {
	Print listPrint        `json:"p"`
	Skip  bool             `json:"x,omitempty"`
	Info  SessionOrderInfo `json:"i"`
}

type listIndex struct {
	Format  int                       `json:"format"`
	Entries map[string]listIndexEntry `json:"entries"`
}

// stampSet answers fileStamp for the files of one folder from the listing that
// was already read.
type stampSet struct{ byName map[string]fs.DirEntry }

func newStampSet(entries []fs.DirEntry) stampSet {
	by := make(map[string]fs.DirEntry, len(entries))
	for _, e := range entries {
		by[e.Name()] = e
	}
	return stampSet{byName: by}
}

func (s stampSet) stamp(name string) fileStamp {
	e, ok := s.byName[name]
	if !ok || e.IsDir() {
		return fileStamp{}
	}
	info, err := e.Info()
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{Size: info.Size(), Mod: info.ModTime().UnixNano(), Here: true}
}

// of is the print of a session's files, and false for a session no list shows:
// one whose removal is pending.
func (s stampSet) of(e fs.DirEntry, full string) (listPrint, bool) {
	if _, pending := s.byName[filepath.Base(store.SessionCleanupPending(full))]; pending {
		return listPrint{}, false
	}
	return listPrint{
		Transcript: s.stamp(e.Name()),
		Meta:       s.stamp(filepath.Base(store.SessionMeta(full))),
		Events:     s.stamp(filepath.Base(store.SessionEventLog(full))),
	}, true
}

// listIndexMu keeps two listings of one process from writing the file at once;
// the write itself is atomic, so a reader never sees half of it.
var listIndexMu sync.Mutex

func loadListIndex(dir string) listIndex {
	raw, err := os.ReadFile(filepath.Join(dir, listIndexName))
	if err != nil {
		return listIndex{}
	}
	var idx listIndex
	if json.Unmarshal(raw, &idx) != nil || idx.Format != listIndexFormat {
		return listIndex{}
	}
	return idx
}

// saveListIndex remembers this listing when it differs from the last one and the
// folder is big enough to matter. It is best effort: a folder that cannot be
// written just reads in full next time.
func saveListIndex(dir string, was listIndex, now map[string]listIndexEntry, reused int) {
	if len(now) < listIndexMin || (reused == len(now) && len(was.Entries) == len(now)) {
		return
	}
	raw, err := json.Marshal(listIndex{Format: listIndexFormat, Entries: now})
	if err != nil {
		return
	}
	listIndexMu.Lock()
	defer listIndexMu.Unlock()
	tmp, err := os.CreateTemp(dir, listIndexName+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(raw)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(dir, listIndexName)) != nil {
		_ = os.Remove(tmp.Name())
	}
}
