package sessionstore

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/state/sessionv4"
)

func TestClassifySkipReadsTheSentinelNotTheSentence(t *testing.T) {
	cases := []struct {
		err  error
		want SkipReason
	}{
		{fmt.Errorf("x: %w", fs.ErrPermission), SkipPermission},
		{fmt.Errorf("%w: %w", sessionv4.ErrDamaged, sessionv4.ErrTooLarge), SkipTooLarge},
		{fmt.Errorf("w: %w", bufio.ErrTooLong), SkipTooLarge},
		{fmt.Errorf("%w: revision 9", sessionv4.ErrUnsupported), SkipSchemaUnsupported},
		{fmt.Errorf("%w: frame magic", sessionv4.ErrDamaged), SkipCorrupt},
		{fmt.Errorf("no sentinel on a read"), SkipCorrupt},
	}
	for _, c := range cases {
		if got := classifySkip(c.err); got != c.want {
			t.Errorf("%v classified %q, want %q", c.err, got, c.want)
		}
	}
}

func TestClassifyWriteSkipKeepsCopyFailedForWrites(t *testing.T) {
	if got := classifyWriteSkip(fmt.Errorf("disk full")); got != SkipCopyFailed {
		t.Errorf("write failure classified %q, want copy_failed", got)
	}
	if got := classifyWriteSkip(fmt.Errorf("x: %w", fs.ErrPermission)); got != SkipPermission {
		t.Errorf("denied write classified %q, want permission", got)
	}
}

func TestImportLegacySessionsDoesNotReportSidecarsAsUnreadableSessions(t *testing.T) {
	src := testenv.TempDir(t)
	dest := testenv.TempDir(t)
	os.WriteFile(filepath.Join(src, "chat.jsonl"), []byte(legacyMessageLog), 0o644)
	os.WriteFile(filepath.Join(src, "chat.turns.jsonl"), []byte(`{"turn":1,"usage":{}}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(src, "chat.guardian.jsonl"), []byte(`{"verdict":"ok"}`+"\n"), 0o644)

	n, rep, err := ImportLegacySessionsFromExplicitDir(src, dest, nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported %d, want 1", n)
	}
	if len(rep.Skipped) != 0 {
		t.Errorf("sidecars reported as skipped sessions: %+v", rep.Skipped)
	}
}
