package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// TestCloneSessionIncludesAuthoritativeEventLog builds a real session where
// the checkpoint lags behind the event log: the clone must carry the turns
// that only exist in the event log.
func TestCloneSessionIncludesAuthoritativeEventLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(src, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := []provider.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
	}
	s := &Session{Messages: msgs}
	if err := s.Save(src); err != nil {
		t.Fatal(err)
	}
	// A newer turn lands only in the event log: the checkpoint (jsonl) stays
	// behind because the log is authoritative once present.
	withThird := append(append([]provider.Message(nil), msgs...), provider.Message{Role: "assistant", Content: "third"})
	digest, _, err := digestAndSizeSessionMessages(withThird)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendSessionReplaceEvent(src, withThird, digest, 0, "test"); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "copy.jsonl")
	if err := CloneSessionToPath(src, dst); err != nil {
		t.Fatal(err)
	}
	cloned, err := LoadSession(dst)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range cloned.Messages {
		texts = append(texts, m.Content)
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(joined, want) {
			t.Errorf("clone missing %q: %v", want, texts)
		}
	}
	if _, err := os.Stat(SessionEventLogPath(dst)); err != nil {
		t.Errorf("clone has no event log: %v", err)
	}
	if _, err := os.Stat(BranchMetaPath(dst)); err != nil {
		t.Errorf("clone has no branch metadata: %v", err)
	}
}

func TestCloneSessionFailureCleansUpPartialCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(src, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{Messages: []provider.Message{{Role: "user", Content: "hi"}}}
	if err := s.Save(src); err != nil {
		t.Fatal(err)
	}
	// An existing destination must be refused (O_EXCL) without touching it.
	dst := filepath.Join(dir, "existing.jsonl")
	if err := os.WriteFile(dst, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CloneSessionToPath(src, dst); err == nil {
		t.Fatal("clone over an existing file must fail")
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "keep" {
		t.Errorf("existing destination was modified: %q", b)
	}
}
