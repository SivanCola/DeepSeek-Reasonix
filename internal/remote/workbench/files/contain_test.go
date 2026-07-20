package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRelRejectsEscapeAndAbsolute(t *testing.T) {
	ws := t.TempDir()
	if _, err := ResolveRel(ws, "../etc/passwd"); err == nil {
		t.Fatal("expected escape rejection")
	}
	if _, err := ResolveRel(ws, "/etc/passwd"); err == nil {
		t.Fatal("expected absolute rejection")
	}
	got, err := ResolveRel(ws, "src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "src", "main.go")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolveRelRejectsSymlinkLeaf(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "leak")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRel(ws, "leak"); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestWriteFileAtomicRoundTrip(t *testing.T) {
	ws := t.TempDir()
	if err := WriteFileAtomic(ws, "a/b.txt", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := ReadFile(ws, "a/b.txt", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("body = %q", data)
	}
}
