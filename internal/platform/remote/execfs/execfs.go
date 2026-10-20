package execfs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"reasonix/internal/platform/remote/sftpfs"
)

// Result is what one script produced.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs body under /bin/sh with args as $1... and stdin. A non-zero exit
// is a Result; the error is for a transport that could not run it at all.
type Runner interface {
	Script(ctx context.Context, stdin io.Reader, body string, args ...string) (Result, error)
}

// TransferCap bounds one whole-file transfer. The bytes pass through memory on
// this side, so the bound is what keeps a stray multi-gigabyte path from
// becoming an allocation.
const TransferCap = 256 << 20

var transferCap int64 = TransferCap

var (
	// ErrToolMissing: the machine lacks a program a script needs. The wrapped
	// message names it.
	ErrToolMissing = errors.New("execfs: a required tool is missing on the machine")
	// ErrTooLarge: the file is over TransferCap.
	ErrTooLarge = errors.New("execfs: the file is over the transfer limit")
)

// Exit statuses of the scripts. Anything else non-zero is a failure whose text
// belongs to the tool and is kept for the log, never matched.
const (
	exitMissing     = 44
	exitIsDir       = 45
	exitToolMissing = 46
	exitTooLarge    = 47
	exitDenied      = 48
)

// needs prefixes a script with a check that every program it execs exists, so
// a stripped-down userland answers with the tool's name instead of a shell's
// "not found" buried in stderr.
func needs(tools, body string) string {
	return `for t in ` + tools + `; do command -v "$t" >/dev/null 2>&1 || { printf %s "$t"; exit 46; }; done
` + body
}

var (
	statScript = needs("stat", `[ -e "$1" ] || exit 44
exec stat -L -c '%s %f %Y' -- "$1"`)

	readScript = needs("head", `[ -e "$1" ] || exit 44
[ -d "$1" ] && exit 45
exec head -c "$2" -- "$1"`)

	downloadScript = needs("stat head", `[ -e "$1" ] || exit 44
[ -d "$1" ] && exit 45
[ "$(stat -L -c %s -- "$1")" -le "$2" ] || exit 47
exec head -c "$2" -- "$1"`)

	writeScript = needs("cat chmod mv rm wc dirname basename", `t="$(dirname -- "$1")/.$(basename -- "$1").reasonix-tmp-$4"
umask 077
set -C
exec 3> "$t" || exit 1
set +C
{ cat >&3 && exec 3>&- && chmod "$2" "$t" && [ "$(wc -c < "$t")" -eq "$3" ] && mv -f -- "$t" "$1"; } || { rm -f -- "$t"; exit 1; }`)

	mkdirAllScript = needs("mkdir", `exec mkdir -p -- "$1"`)
	mkdirScript    = needs("mkdir", `exec mkdir -- "$1"`)
	chmodScript    = needs("chmod", `[ -e "$1" ] || exit 44
exec chmod "$2" -- "$1"`)

	removeScript = needs("rm rmdir", `[ -e "$1" ] || [ -L "$1" ] || exit 44
if [ -d "$1" ] && [ ! -L "$1" ]; then
  if [ "$2" = 1 ]; then exec rm -rf -- "$1"; else exec rmdir -- "$1"; fi
fi
exec rm -f -- "$1"`)

	realPathScript = needs("readlink", `case "$1" in
"~") p=$HOME ;;
"~/"*) p=$HOME/${1#"~/"} ;;
*) p=$1 ;;
esac
exec readlink -f -- "$p"`)

	// Each entry is a kind byte (d: directory, symlinks to one included; f:
	// anything else) and the name, closed by a NUL so no name can end it early.
	listScript = `[ -d "$1" ] || { [ -e "$1" ] && exit 45; exit 44; }
[ -r "$1" ] && [ -x "$1" ] || exit 48
cd -- "$1" || exit 1
for f in ./* ./.[!.]* ./..?*; do
  [ -e "$f" ] || [ -L "$f" ] || continue
  if [ -d "$f" ]; then printf 'd%s\0' "${f#./}"; else printf 'f%s\0' "${f#./}"; fi
done
exit 0`
)

// FS is the file layer over a Runner.
type FS struct{ r Runner }

var _ sftpfs.Volume = (*FS)(nil)

// New binds the file layer to a Runner.
func New(r Runner) *FS { return &FS{r: r} }

// Mode says which transport carries the bytes.
func (f *FS) Mode() sftpfs.Mode { return sftpfs.ModeExec }

// Close is a no-op: the connection belongs to the Runner's owner.
func (f *FS) Close() error { return nil }

func (f *FS) run(ctx context.Context, op string, stdin io.Reader, body string, args ...string) (Result, error) {
	res, err := f.r.Script(ctx, stdin, body, args...)
	if err != nil {
		return res, err
	}
	return res, failure(op, res)
}

func failure(op string, res Result) error {
	switch res.ExitCode {
	case 0:
		return nil
	case exitMissing:
		return fmt.Errorf("%s: %w", op, fs.ErrNotExist)
	case exitIsDir:
		return fmt.Errorf("%s: is a directory", op)
	case exitToolMissing:
		return fmt.Errorf("%s: %w: %s", op, ErrToolMissing, strings.TrimSpace(string(res.Stdout)))
	case exitDenied:
		return fmt.Errorf("%s: %w", op, fs.ErrPermission)
	case exitTooLarge:
		return fmt.Errorf("%s: %w", op, ErrTooLarge)
	}
	return fmt.Errorf("%s: exit %d: %s", op, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
}

func (f *FS) Stat(ctx context.Context, p string) (sftpfs.Entry, error) {
	res, err := f.run(ctx, "stat "+p, nil, statScript, p)
	if err != nil {
		return sftpfs.Entry{}, err
	}
	var size, mtime int64
	var rawMode uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(res.Stdout)), "%d %x %d", &size, &rawMode, &mtime); err != nil {
		return sftpfs.Entry{}, fmt.Errorf("stat %s: unreadable answer: %w", p, err)
	}
	mode := FileMode(uint32(rawMode))
	return sftpfs.Entry{Name: path.Base(p), Path: p, Size: size, Mode: mode, ModTime: mtime, IsDir: mode.IsDir()}, nil
}

// FileMode decodes a raw st_mode by its bit fields: the format bits say what
// the file is, the low twelve are the permissions.
func FileMode(raw uint32) fs.FileMode {
	mode := fs.FileMode(raw & 0o777)
	if raw&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}
	if raw&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}
	if raw&0o1000 != 0 {
		mode |= fs.ModeSticky
	}
	switch raw & 0o170000 {
	case 0o040000:
		mode |= fs.ModeDir
	case 0o120000:
		mode |= fs.ModeSymlink
	case 0o010000:
		mode |= fs.ModeNamedPipe
	case 0o140000:
		mode |= fs.ModeSocket
	case 0o020000:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case 0o060000:
		mode |= fs.ModeDevice
	}
	return mode
}

func (f *FS) ReadFile(ctx context.Context, p string, maxSize int64) ([]byte, bool, sftpfs.Kind, error) {
	if maxSize <= 0 {
		maxSize = sftpfs.DefaultReadCap
	}
	res, err := f.run(ctx, "read "+p, nil, readScript, p, strconv.FormatInt(maxSize+1, 10))
	if err != nil {
		return nil, false, sftpfs.KindBinary, err
	}
	data, truncated := res.Stdout, false
	if int64(len(data)) > maxSize {
		data, truncated = data[:maxSize], true
	}
	return data, truncated, sftpfs.DetectKind(data), nil
}

// Download copies the whole file to w, up to TransferCap; a larger file is
// ErrTooLarge before any byte is read.
func (f *FS) Download(ctx context.Context, p string, w io.Writer) (int64, error) {
	res, err := f.run(ctx, "read "+p, nil, downloadScript, p, strconv.FormatInt(transferCap, 10))
	if err != nil {
		return 0, err
	}
	n, err := w.Write(res.Stdout)
	return int64(n), err
}

// tempNonce names the temporary file; it is unguessable so nobody can plant a
// link at it ahead of time, and the script opens it exclusively regardless.
var tempNonce = func() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (f *FS) WriteFileAtomic(ctx context.Context, p string, data []byte, perm fs.FileMode) error {
	if int64(len(data)) > transferCap {
		return fmt.Errorf("write %s: %w", p, ErrTooLarge)
	}
	_, err := f.run(ctx, "write "+p, bytes.NewReader(data), writeScript, p,
		strconv.FormatUint(uint64(perm.Perm()), 8), strconv.Itoa(len(data)), tempNonce())
	return err
}

// UploadAtomic reads r whole, up to TransferCap, so the script can check that
// every byte arrived before it publishes the file.
func (f *FS) UploadAtomic(ctx context.Context, p string, r io.Reader, perm fs.FileMode) (int64, error) {
	data, err := io.ReadAll(io.LimitReader(r, transferCap+1))
	if err != nil {
		return 0, err
	}
	if err := f.WriteFileAtomic(ctx, p, data, perm); err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}

func (f *FS) MkdirAll(ctx context.Context, p string) error {
	_, err := f.run(ctx, "mkdir "+p, nil, mkdirAllScript, p)
	return err
}

func (f *FS) MkdirExclusive(ctx context.Context, p string) error {
	_, err := f.run(ctx, "mkdir "+p, nil, mkdirScript, p)
	return err
}

func (f *FS) Chmod(ctx context.Context, p string, perm fs.FileMode) error {
	_, err := f.run(ctx, "chmod "+p, nil, chmodScript, p, strconv.FormatUint(uint64(perm.Perm()), 8))
	return err
}

func (f *FS) Remove(ctx context.Context, p string, recursive bool) error {
	flag := "0"
	if recursive {
		flag = "1"
	}
	_, err := f.run(ctx, "remove "+p, nil, removeScript, p, flag)
	return err
}

func (f *FS) RealPath(ctx context.Context, p string) (string, error) {
	res, err := f.run(ctx, "realpath "+p, nil, realPathScript, p)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(res.Stdout), "\n"), nil
}

// List returns the entries of dir. Only the name and whether it is a directory
// are known; a symlink to a directory counts as one.
func (f *FS) List(ctx context.Context, dir string) ([]sftpfs.Entry, error) {
	res, err := f.run(ctx, "list "+dir, nil, listScript, dir)
	if err != nil {
		return nil, err
	}
	var out []sftpfs.Entry
	for rec := range strings.SplitSeq(string(res.Stdout), "\x00") {
		if len(rec) < 2 {
			continue
		}
		e := sftpfs.Entry{Name: rec[1:], Path: path.Join(dir, rec[1:])}
		if rec[0] == 'd' {
			e.IsDir, e.Mode = true, fs.ModeDir
		}
		out = append(out, e)
	}
	return out, nil
}
