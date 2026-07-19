package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
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

func TestSaveExportImageFilesWritesNumberedParts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.archive.png")
	payloads := [][]byte{{0x01, 0x02}, {0x03, 0x04}, {0x05, 0x06}}
	encoded := make([]string, len(payloads))
	for i, payload := range payloads {
		encoded[i] = base64.StdEncoding.EncodeToString(payload)
	}

	if err := (&App{}).SaveExportImageFiles(path, encoded); err != nil {
		t.Fatalf("save image parts: %v", err)
	}
	for i, want := range payloads {
		partPath := filepath.Join(dir, fmt.Sprintf("session.archive-%d-of-3.png", i+1))
		got, err := os.ReadFile(partPath)
		if err != nil {
			t.Fatalf("read image part %d: %v", i+1, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("image part %d = %v, want %v", i+1, got, want)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("multi-part export should not write the selected base path, stat error = %v", err)
	}
}

func TestSaveExportImageFilesRejectsCollisionWithoutPartialOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.png")
	collisionPath := filepath.Join(dir, "session-2-of-3.png")
	if err := os.WriteFile(collisionPath, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seed collision: %v", err)
	}
	payload := base64.StdEncoding.EncodeToString([]byte("new image"))

	err := (&App{}).SaveExportImageFiles(path, []string{payload, payload, payload})
	if err == nil {
		t.Fatal("expected existing numbered export to reject the batch")
	}
	if got, readErr := os.ReadFile(collisionPath); readErr != nil || string(got) != "keep me" {
		t.Fatalf("existing image part changed: data=%q err=%v", got, readErr)
	}
	for _, name := range []string{"session-1-of-3.png", "session-3-of-3.png"} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(statErr) {
			t.Fatalf("collision should leave no partial output %s, stat error = %v", name, statErr)
		}
	}
}

func TestSaveExportImageFilesDecodesAllPartsBeforeWriting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.png")
	valid := base64.StdEncoding.EncodeToString([]byte("image"))

	err := (&App{}).SaveExportImageFiles(path, []string{valid, "not base64!", valid})
	if err == nil {
		t.Fatal("expected invalid image payload to reject the batch")
	}
	for i := 1; i <= 3; i++ {
		partPath := filepath.Join(dir, fmt.Sprintf("session-%d-of-3.png", i))
		if _, statErr := os.Stat(partPath); !os.IsNotExist(statErr) {
			t.Fatalf("invalid payload should leave no image part %d, stat error = %v", i, statErr)
		}
	}
}

func TestSaveExclusiveExportFilesRollsBackCommittedTargets(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "duplicate.png")

	err := saveExclusiveExportFiles(
		[]string{target, target},
		[][]byte{[]byte("first"), []byte("second")},
	)
	if err == nil {
		t.Fatal("expected duplicate exclusive target to fail")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("failed batch should roll back its committed target, stat error = %v", statErr)
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
