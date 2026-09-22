package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reasonix/internal/fileops"
	"reflect"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func TestDisplayPagerRejectsSameSizeRewriteWithRestoredMtime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.jsonl")
	before := []byte("{\"role\":\"user\",\"content\":\"one\"}\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "index.sqlite")
	p, err := OpenDisplayPager(t.Context(), path, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"two\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_, version := fileops.DiskSnapshot(path, current)
	if version == p.sourceVersion {
		t.Skip("filesystem does not expose a change-time version")
	}
	if err := p.Validate(); !errors.Is(err, ErrDisplaySourceChanged) {
		t.Fatalf("same-size source replacement accepted: %v", err)
	}
	oldDigest := p.Header.ContentDigest
	p.Close()
	next, err := OpenDisplayPager(t.Context(), path, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Header.ContentDigest == oldDigest {
		t.Fatal("persistent cache trusted size and restored mtime alone")
	}
}

func TestDisplayPagerRebuildsUntrustedSidecarWithoutModifyingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.jsonl")
	body := []byte("{\"role\":\"user\",\"content\":\"original\"}\n")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	sidecar := store.SessionDisplayIndex(path)
	if err := os.WriteFile(sidecar, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := OpenDisplayPager(t.Context(), path, filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Header.MessageCount != 1 {
		t.Fatalf("rebuilt count=%d", p.Header.MessageCount)
	}
	after, _ := os.ReadFile(path)
	idx, _ := os.ReadFile(sidecar)
	if string(after) != string(body) || string(idx) != "{broken" {
		t.Fatal("display preparation repaired authoritative storage or its sidecar")
	}
}

func TestDisplayPagerCheckpointIsBoundedAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.jsonl")
	var original []byte
	for _, m := range displayIndexTestMessages() {
		body, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		original = append(original, append(body, '\n')...)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "display.sqlite")
	p, err := OpenDisplayPager(t.Context(), path, cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Header.Entries) != 0 || p.Header.MessageCount != len(displayIndexTestMessages()) || p.Header.AuthoredTurns != 2 {
		t.Fatalf("unbounded or incorrect header: %+v", p.Header)
	}
	entries, err := p.Entries(1, 3)
	if err != nil || len(entries) != 2 || entries[0].Role != provider.RoleUser {
		t.Fatalf("page: %+v %v", entries, err)
	}
	if _, err := p.Entries(0, 501); err == nil {
		t.Fatal("unbounded page accepted")
	}
	digest := p.Header.ContentDigest
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	p, err = OpenDisplayPager(t.Context(), path, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Header.ContentDigest != digest {
		t.Fatal("reopening changed snapshot identity")
	}
	got, _ := os.ReadFile(path)
	if !reflect.DeepEqual(got, original) {
		t.Fatal("display preparation changed authoritative content")
	}
	if _, err := os.Stat(store.SessionDisplayIndex(path)); !os.IsNotExist(err) {
		t.Fatal("read created a session sidecar")
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err == nil {
		t.Fatal("source replacement retained stale offsets")
	}
}

func TestDisplayPagerCancellationDoesNotPublishPartialGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"one\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "display.sqlite")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if p, err := OpenDisplayPager(ctx, path, cache); err == nil {
		p.Close()
		t.Fatal("cancelled build succeeded")
	}
	p, err := OpenDisplayPager(t.Context(), path, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Header.MessageCount != 1 {
		t.Fatal("cancelled generation was published")
	}
}

func TestDisplayPagerRejectsNewEventAuthority(t *testing.T) {
	for _, existingEmpty := range []bool{false, true} {
		t.Run(fmt.Sprint(existingEmpty), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checkpoint.jsonl")
			if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"one\"}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			log := store.SessionEventLog(path)
			if existingEmpty {
				if err := os.WriteFile(log, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			p, err := OpenDisplayPager(t.Context(), path, filepath.Join(t.TempDir(), "index.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			// No checkpoint byte or timestamp changes. Even an incomplete new
			// event log prevents the old view from claiming complete authority.
			if err := os.WriteFile(log, []byte("{\"schema_version\":2}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := p.Validate(); !errors.Is(err, ErrDisplaySourceChanged) {
				t.Fatalf("new event authority accepted: %v", err)
			}
		})
	}
}
