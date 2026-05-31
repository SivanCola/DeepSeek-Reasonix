package control

import (
	"os"
	"strings"
	"testing"
)

func TestSaveImageDataURL(t *testing.T) {
	t.Chdir(t.TempDir())

	got, err := SaveImageDataURL("data:image/png;base64,iVBORw0KGgo=")
	if err != nil {
		t.Fatalf("SaveImageDataURL: %v", err)
	}
	if !strings.HasPrefix(got, ".reasonix/attachments/clipboard-") || !strings.HasSuffix(got, ".png") {
		t.Fatalf("path = %q, want attachment png path", got)
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("saved bytes = %q, err=%v", string(b), err)
	}
}

func TestSaveImageDataURLRejectsUnsupportedMime(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := SaveImageDataURL("data:text/plain;base64,aGk="); err == nil {
		t.Fatal("unsupported mime should fail")
	}
}

func TestImageDataURL(t *testing.T) {
	t.Chdir(t.TempDir())
	path, err := SaveImageDataURL("data:image/png;base64,iVBORw0KGgo=")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ImageDataURL(path)
	if err != nil {
		t.Fatalf("ImageDataURL: %v", err)
	}
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("data url = %q", got)
	}
}

func TestImageDataURLRejectsOutsideAttachmentDir(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("x.png", []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImageDataURL("x.png"); err == nil {
		t.Fatal("outside attachment dir should fail")
	}
}
