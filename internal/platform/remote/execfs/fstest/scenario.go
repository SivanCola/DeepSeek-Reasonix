// Package fstest holds the scenario every execfs runner must pass against a
// real POSIX userland: the local one in CI, a distribution or an SSH server in
// the live tests.
package fstest

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"

	"reasonix/internal/platform/remote/sftpfs"
)

// RunScenario drives the file layer against a real POSIX shell: the local
// one in CI, a distribution in the live test. dir is a scratch directory there.
func RunScenario(t *testing.T, fsys sftpfs.Volume, dir string) {
	t.Helper()
	ctx := context.Background()
	awkward := path.Join(dir, `a b "q" 'x' $HOME `+"`z`"+` ünï back\slash trailing\ -dash`)

	if err := fsys.MkdirAll(ctx, awkward+"/deep/er"); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsys.MkdirExclusive(ctx, awkward+"/deep"); err == nil {
		t.Fatal("MkdirExclusive on an existing directory succeeded")
	}
	if err := fsys.MkdirExclusive(ctx, awkward+"/lock"); err != nil {
		t.Fatalf("MkdirExclusive: %v", err)
	}

	if err := fsys.MkdirAll(ctx, awkward+"/-dash"); err != nil {
		t.Fatalf("MkdirAll of a component that starts with a dash: %v", err)
	}
	file := awkward + "/-dash/state.json"
	want := []byte("{\"port\": 4242}\n")
	if err := fsys.WriteFileAtomic(ctx, file, want, 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if err := fsys.WriteFileAtomic(ctx, file, append(want, 'x'), 0o600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, truncated, kind, err := fsys.ReadFile(ctx, file, 0)
	if err != nil || truncated || kind != sftpfs.KindText || !bytes.Equal(got, append(want, 'x')) {
		t.Fatalf("ReadFile = %q, %v, %v, %v", got, truncated, kind, err)
	}
	if head, truncated, _, err := fsys.ReadFile(ctx, file, 4); err != nil || !truncated || string(head) != "{\"po" {
		t.Fatalf("a capped read = %q, %v, %v", head, truncated, err)
	}
	en, err := fsys.Stat(ctx, file)
	if err != nil || en.IsDir || en.Size != int64(len(want)+1) || en.Mode.Perm() != 0o600 || en.ModTime == 0 {
		t.Fatalf("Stat file = %+v, %v", en, err)
	}
	if en, err := fsys.Stat(ctx, awkward); err != nil || !en.IsDir {
		t.Fatalf("Stat dir = %+v, %v", en, err)
	}
	if _, _, _, err := fsys.ReadFile(ctx, awkward+"/deep", 0); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reading a directory = %v, want a failure that is not 'missing'", err)
	}

	if err := fsys.Chmod(ctx, file, 0o640); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if en, _ := fsys.Stat(ctx, file); en.Mode.Perm() != 0o640 {
		t.Fatalf("mode after chmod = %v", en.Mode)
	}

	missingAndGone(t, fsys, awkward, file)
	hostileNames(t, fsys, dir)
	transfers(t, fsys, dir)
}

// hostileNames sends names that would change meaning if any layer treated them
// as shell text, and checks each came back as the same bytes and nothing else
// was created beside them.
func hostileNames(t *testing.T, fsys sftpfs.Volume, dir string) {
	t.Helper()
	ctx := context.Background()
	root := dir + "/hostile"
	if err := fsys.MkdirAll(ctx, root); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"sp ace", `dq"uote`, "sq'uote", "semi; rm -rf x", "-lead", "--", "new\nline", "$(touch pwned)", "`touch pwned`",
		"*glob", "~tilde", "a&b|c>d<e", "back\\slash", strings.Repeat("l", 200),
	}
	for _, name := range names {
		p := root + "/" + name
		body := []byte("body of " + name)
		if err := fsys.WriteFileAtomic(ctx, p, body, 0o600); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
		got, _, _, err := fsys.ReadFile(ctx, p, 0)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("read %q = %q, %v", name, got, err)
		}
		if en, err := fsys.Stat(ctx, p); err != nil || en.Size != int64(len(body)) {
			t.Fatalf("stat %q = %+v, %v", name, en, err)
		}
	}
	entries, err := fsys.List(ctx, root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Name] = true
		if e.IsDir {
			t.Errorf("%q listed as a directory", e.Name)
		}
	}
	if len(entries) != len(names) {
		t.Errorf("List holds %d entries, want exactly the %d written", len(entries), len(names))
	}
	for _, name := range names {
		if !seen[name] {
			t.Errorf("List lost %q", name)
		}
	}
	for _, name := range names {
		if err := fsys.Remove(ctx, root+"/"+name, false); err != nil {
			t.Errorf("remove %q: %v", name, err)
		}
	}

	deep := root + strings.Repeat("/"+strings.Repeat("d", 200), 15)
	if err := fsys.MkdirAll(ctx, deep); err != nil {
		t.Fatalf("a %d-byte path: %v", len(deep), err)
	}
	if err := fsys.WriteFileAtomic(ctx, deep+"/f", []byte("x"), 0o600); err != nil {
		t.Fatalf("write at the end of a %d-byte path: %v", len(deep), err)
	}
	if _, err := fsys.Stat(ctx, deep+"/f"); err != nil {
		t.Fatalf("stat at the end of a %d-byte path: %v", len(deep), err)
	}
}

func transfers(t *testing.T, fsys sftpfs.Volume, dir string) {
	t.Helper()
	ctx := context.Background()
	if err := fsys.MkdirAll(ctx, dir+"/xfer/sub dir"); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte{0, 1, 2, 255, '\n', '\'', 'x'}, 40000)
	n, err := fsys.UploadAtomic(ctx, dir+"/xfer/it's.bin", bytes.NewReader(payload), 0o640)
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("UploadAtomic = %d, %v", n, err)
	}
	var back bytes.Buffer
	if n, err := fsys.Download(ctx, dir+"/xfer/it's.bin", &back); err != nil || n != int64(len(payload)) || !bytes.Equal(back.Bytes(), payload) {
		t.Fatalf("Download = %d, %v, equal=%v", n, err, bytes.Equal(back.Bytes(), payload))
	}
	if en, err := fsys.Stat(ctx, dir+"/xfer/it's.bin"); err != nil || en.Mode.Perm() != 0o640 {
		t.Fatalf("uploaded mode = %+v, %v", en, err)
	}
	if _, err := fsys.Download(ctx, dir+"/xfer/none", &back); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Download of a missing file = %v, want fs.ErrNotExist", err)
	}
	if _, err := fsys.Download(ctx, dir+"/xfer/sub dir", &back); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Download of a directory = %v, want a failure that is not 'missing'", err)
	}
	if _, err := fsys.List(ctx, dir+"/xfer/none"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("List of a missing directory = %v, want fs.ErrNotExist", err)
	}
	if _, err := fsys.List(ctx, dir+"/xfer/it's.bin"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("List of a file = %v, want a failure that is not 'missing'", err)
	}
	if os.Geteuid() != 0 {
		if err := fsys.MkdirAll(ctx, dir+"/xfer/shut"); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Chmod(ctx, dir+"/xfer/shut", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := fsys.List(ctx, dir+"/xfer/shut"); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("List of a directory this account cannot read = %v, want fs.ErrPermission", err)
		}
		if at, _, err := sftpfs.Dirs(ctx, fsys, dir+"/xfer/shut"); !errors.Is(err, sftpfs.ErrNotListable) || !strings.HasSuffix(at, "/shut") {
			t.Errorf("Dirs of it = %q, %v, want the directory itself and ErrNotListable", at, err)
		}
		if err := fsys.Remove(ctx, dir+"/xfer/shut", false); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := fsys.List(ctx, dir+"/xfer")
	if err != nil || len(entries) != 2 {
		t.Fatalf("List = %+v, %v", entries, err)
	}
	for _, e := range entries {
		if e.IsDir != (e.Name == "sub dir") {
			t.Errorf("%q: IsDir = %v", e.Name, e.IsDir)
		}
	}
}

// missingAndGone holds what a path that is not there answers, then tears the
// scenario's tree down and checks it is gone.
func missingAndGone(t *testing.T, fsys sftpfs.Volume, awkward, file string) {
	t.Helper()
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"stat":   func() error { _, err := fsys.Stat(ctx, awkward+"/none"); return err },
		"read":   func() error { _, _, _, err := fsys.ReadFile(ctx, awkward+"/none", 0); return err },
		"chmod":  func() error { return fsys.Chmod(ctx, awkward+"/none", 0o600) },
		"remove": func() error { return fsys.Remove(ctx, awkward+"/none", false) },
	} {
		if err := call(); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s of a missing path = %v, want fs.ErrNotExist", name, err)
		}
	}

	real, err := fsys.RealPath(ctx, awkward+"/deep/../-dash/state.json")
	if err != nil || !strings.HasSuffix(real, "/-dash/state.json") || strings.Contains(real, "..") {
		t.Fatalf("RealPath = %q, %v", real, err)
	}
	if home, err := fsys.RealPath(ctx, "~"); err != nil || !strings.HasPrefix(home, "/") {
		t.Fatalf("RealPath(~) = %q, %v", home, err)
	}

	if err := fsys.Remove(ctx, awkward+"/deep", false); err == nil {
		t.Fatal("a non-recursive remove of a non-empty directory succeeded")
	}
	if err := fsys.Remove(ctx, file, false); err != nil {
		t.Fatalf("Remove file: %v", err)
	}
	if err := fsys.Remove(ctx, awkward, true); err != nil {
		t.Fatalf("Remove tree: %v", err)
	}
	if _, err := fsys.Stat(ctx, awkward); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the removed tree is still there: %v", err)
	}
}
