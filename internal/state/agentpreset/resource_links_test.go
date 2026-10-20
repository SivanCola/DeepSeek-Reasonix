package agentpreset

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/contract/agentcomposition"
)

func linkAsset(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
}

func TestCaptureDirectoryMaterializesContainedLinks(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data", "script.sh"), []byte("original"), 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(source, "data", "script.sh"))
	if err != nil {
		t.Fatal(err)
	}
	wantMode := uint32(info.Mode().Perm() & 0o700)
	linkAsset(t, "data/script.sh", filepath.Join(source, "relative.sh"))
	linkAsset(t, filepath.Join(source, "data", "script.sh"), filepath.Join(source, "absolute.sh"))
	linkAsset(t, "data", filepath.Join(source, "alias"))
	entry := filepath.Join(t.TempDir(), "linked-root")
	linkAsset(t, source, entry)
	snapshot, err := CaptureDirectory(entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 4 {
		t.Fatalf("snapshot lost aliases: %+v", snapshot)
	}
	for _, f := range snapshot.Files {
		if f.Link != "" || string(f.Data) != "original" || f.Mode != wantMode {
			t.Fatalf("invalid retained file: %+v", f)
		}
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	store := New(t.TempDir(), t.TempDir())
	root, err := store.Materialize(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize(snapshot); err != nil {
		t.Fatalf("restart rejected flattened snapshot: %v", err)
	}
	for _, f := range snapshot.Files {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("snapshot retained a link: %v", err)
		}
	}
}

func TestCaptureDirectoryRefusesUnsafeLinks(t *testing.T) {
	for _, kind := range []string{"escape", "broken", "cycle", "ancestor", "secret", "vcs"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			target := "missing"
			switch kind {
			case "escape":
				target = filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "cycle":
				target = "alias"
			case "ancestor":
				target = "."
			case "secret":
				target = ".env"
				if err := os.WriteFile(filepath.Join(root, target), []byte("secret"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "vcs":
				target = ".git"
				if err := os.Mkdir(filepath.Join(root, target), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			linkAsset(t, target, filepath.Join(root, "alias"))
			if _, err := CaptureDirectory(root); !errors.Is(err, agentcomposition.ErrUnavailable) {
				t.Fatalf("unsafe %s link accepted: %v", kind, err)
			}
		})
	}
}

func TestCaptureHookFileFlattensContainedLink(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "data.txt"), []byte("context"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "alias.txt")
	linkAsset(t, "data.txt", path)
	asset, mapping, err := CaptureHookResources([]string{path})
	if err != nil || len(asset.Files) != 1 || mapping[path] == "" || string(asset.Files[0].Data) != "context" {
		t.Fatalf("direct hook file was not retained: %+v %v", asset, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	linkAsset(t, filepath.Join(t.TempDir(), "outside"), path)
	if _, _, err := CaptureHookResources([]string{path}); !errors.Is(err, agentcomposition.ErrUnavailable) {
		t.Fatalf("outside hook link accepted: %v", err)
	}
}

func TestMaterializeStillRejectsLinkedRetainedFilesAndRoot(t *testing.T) {
	for _, rootLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "root"}[rootLink], func(t *testing.T) {
			store := New(t.TempDir(), t.TempDir())
			snapshot := DirectorySnapshot{Files: []AssetFile{{Path: "entry", Data: []byte("same"), Mode: 0o600}}}
			root, err := store.Materialize(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "entry")
			link := filepath.Join(root, "entry")
			if rootLink {
				target, link = filepath.Join(t.TempDir(), "moved"), root
				if err := os.Rename(root, target); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(target, []byte("same"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
			}
			linkAsset(t, target, link)
			if _, err := store.Materialize(snapshot); !errors.Is(err, agentcomposition.ErrUnavailable) {
				t.Fatalf("retained %s link accepted: %v", link, err)
			}
		})
	}
}
