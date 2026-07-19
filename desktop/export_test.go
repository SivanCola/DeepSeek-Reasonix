package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveExportFileWritesTextAndBinaryPayloads(t *testing.T) {
	t.Parallel()
	app := &App{}
	dir := t.TempDir()

	textPath := filepath.Join(dir, "session.md")
	if err := app.SaveExportFile(textPath, "# 会话\n", false); err != nil {
		t.Fatalf("save text export: %v", err)
	}
	text, err := os.ReadFile(textPath)
	if err != nil {
		t.Fatalf("read text export: %v", err)
	}
	if got, want := string(text), "# 会话\n"; got != want {
		t.Fatalf("text export = %q, want %q", got, want)
	}

	binaryPath := filepath.Join(dir, "session.png")
	binary := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0xff}
	if err := app.SaveExportFile(binaryPath, base64.StdEncoding.EncodeToString(binary), true); err != nil {
		t.Fatalf("save binary export: %v", err)
	}
	written, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read binary export: %v", err)
	}
	if string(written) != string(binary) {
		t.Fatalf("binary export = %v, want %v", written, binary)
	}
}

func TestSaveExportFileRejectsInvalidBase64(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "broken.pdf")
	err := (&App{}).SaveExportFile(path, "not base64!", true)
	if err == nil {
		t.Fatal("expected invalid base64 error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("invalid payload should not create a file, stat error = %v", statErr)
	}
}

func TestExportFileFiltersSelectExpectedNativePattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mime string
		ext  string
		want string
	}{
		{mime: "application/pdf", ext: ".pdf", want: "*.pdf"},
		{mime: "image/png", ext: ".png", want: "*.png"},
		{mime: "application/octet-stream", ext: ".bin", want: "*.bin"},
	}
	for _, test := range tests {
		filters := exportFileFilters(test.mime, test.ext)
		if len(filters) != 1 || filters[0].Pattern != test.want {
			t.Fatalf("filters for %s = %#v, want pattern %q", test.mime, filters, test.want)
		}
	}
}
