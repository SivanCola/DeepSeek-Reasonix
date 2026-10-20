package wslconn

import (
	"context"
	"fmt"
	"io"

	"reasonix/internal/platform/remote"
	"reasonix/internal/platform/remote/execfs"
	"reasonix/internal/platform/remote/sftpfs"
	"reasonix/internal/platform/wsl"
)

// Conn is one distribution as a bootstrap connection.
type Conn struct {
	distro string
	run    wsl.Command
}

// New binds a connection to an installed distribution. The name must be one
// wsl.exe lists, which keeps anything else out of its argument list.
func New(ctx context.Context, list wsl.Runner, run wsl.Command, distro string) (*Conn, error) {
	all, err := wsl.List(ctx, list)
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if d.Name == distro {
			if d.Version != 2 {
				return nil, fmt.Errorf("%w: %q is WSL%d", wsl.ErrUnsupported, distro, d.Version)
			}
			return &Conn{distro: distro, run: run}, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", wsl.ErrNoDistro, distro)
}

// Distro is the distribution's name.
func (c *Conn) Distro() string { return c.distro }

// Exec runs cmd under /bin/sh in the distribution, as an SSH exec would run it
// under the account's shell.
func (c *Conn) Exec(ctx context.Context, cmd string) (remote.ExecResult, error) {
	res, err := c.run(ctx, nil, wsl.ExecArgs(c.distro, "/bin/sh", "-c", cmd)...)
	return remote.ExecResult{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}, err
}

// Files is the distribution's file layer.
func (c *Conn) Files() (sftpfs.Files, error) { return c.Volume(), nil }

// Volume is the file layer with listing and whole-file transfer too.
func (c *Conn) Volume() sftpfs.Volume { return execfs.New(runner{c}) }

// Dirs resolves dir (empty or ~ meaning the home directory) and lists what is
// inside it.
func (c *Conn) Dirs(ctx context.Context, dir string) (string, []sftpfs.Entry, error) {
	return sftpfs.Dirs(ctx, c.Volume(), dir)
}

type runner struct{ c *Conn }

func (r runner) Script(ctx context.Context, stdin io.Reader, body string, args ...string) (execfs.Result, error) {
	res, err := r.c.script(ctx, stdin, body, args...)
	return execfs.Result{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}, err
}

// Script runs body under /bin/sh with args as $1... and stdin.
func (c *Conn) Script(ctx context.Context, stdin io.Reader, body string, args ...string) (wsl.Result, error) {
	return c.script(ctx, stdin, body, args...)
}

// Run starts a wsl.exe session that lives until ctx ends, feeding stdin to the
// program argv names, and returns when it ends.
func (c *Conn) Run(ctx context.Context, stdin io.Reader, argv ...string) (wsl.Result, error) {
	return c.run(ctx, stdin, wsl.ExecArgs(c.distro, argv...)...)
}

// script runs body with args as $1... and stdin, returning its result.
func (c *Conn) script(ctx context.Context, stdin io.Reader, body string, args ...string) (wsl.Result, error) {
	argv := append([]string{"/bin/sh", "-c", body, "sh"}, args...)
	return c.run(ctx, stdin, wsl.ExecArgs(c.distro, argv...)...)
}
