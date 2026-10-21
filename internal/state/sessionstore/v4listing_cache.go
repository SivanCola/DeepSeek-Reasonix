package sessionstore

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"

	"reasonix/internal/base/filelock"
	"reasonix/internal/base/fileutil"
	"reasonix/internal/state/sessionv4"
)

// v4ListingName remembers what replaying a 1.x conversation answered. 1.x's own
// cache is missing or stale for many of them, and the store beside the folder
// is read-only to us, so the answer is kept in the folder we own.
const v4ListingName = ".v4-listing.json"

const v4ListingFormat = 2

type v4ListingEntry struct {
	Manifest sessionv4.Manifest `json:"identity"`
	LogSize  int64              `json:"s"`
	LogMod   int64              `json:"m"`
	Listing  sessionv4.Listing  `json:"l"`
}

type v4ListingCache struct {
	Format  int                       `json:"format"`
	Entries map[string]v4ListingEntry `json:"entries"`
	dirty   bool
	changed map[string]v4ListingEntry
}

func loadV4Listings(dir string) *v4ListingCache {
	c := &v4ListingCache{Format: v4ListingFormat, Entries: map[string]v4ListingEntry{}}
	raw, err := os.ReadFile(filepath.Join(dir, v4ListingName))
	if err != nil {
		return c
	}
	var read v4ListingCache
	if json.Unmarshal(raw, &read) != nil || read.Format != v4ListingFormat || read.Entries == nil {
		return c
	}
	c.Entries = read.Entries
	return c
}

func (c *v4ListingCache) get(s sessionv4.Session) (sessionv4.Listing, bool) {
	e, ok := c.Entries[s.Manifest.SessionID]
	if !ok || !e.Manifest.Equal(s.Manifest) || e.LogSize != s.LogSize || e.LogMod != s.LogModTime.UnixNano() {
		return sessionv4.Listing{}, false
	}
	return e.Listing, true
}

func (c *v4ListingCache) put(s sessionv4.Session, l sessionv4.Listing) {
	e := v4ListingEntry{Manifest: s.Manifest, LogSize: s.LogSize, LogMod: s.LogModTime.UnixNano(), Listing: l}
	c.Entries[s.Manifest.SessionID] = e
	if c.changed == nil {
		c.changed = map[string]v4ListingEntry{}
	}
	c.changed[s.Manifest.SessionID] = e
	c.dirty = true
}

func (c *v4ListingCache) save(ctx context.Context, dir string) error {
	if !c.dirty {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	release, err := filelock.TryAcquire(filepath.Join(dir, v4ListingName+".lock"))
	if err != nil {
		return err
	}
	defer release()
	merged := loadV4Listings(dir)
	for id, e := range c.changed {
		s, err := sessionv4.Open(filepath.Join(v4RootFor(dir), id))
		if err == nil && e.Manifest.Equal(s.Manifest) && e.LogSize == s.LogSize && e.LogMod == s.LogModTime.UnixNano() {
			merged.Entries[id] = e
		}
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	if err := fileutil.AtomicWriteFileStrictValidated(filepath.Join(dir, v4ListingName), raw, 0o600, ctx.Err); err != nil {
		return err
	}
	maps.Copy(c.Entries, merged.Entries)
	c.changed = nil
	c.dirty = false
	return nil
}
