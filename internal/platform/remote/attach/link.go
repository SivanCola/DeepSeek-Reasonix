package attach

import (
	"context"

	"reasonix/internal/platform/remote"
	"reasonix/internal/platform/remote/bootstrap"
	"reasonix/internal/platform/remote/forward"
	"reasonix/internal/platform/remote/sftpfs"
)

// Link is one machine's connection as the pool uses it: commands and files for
// the bootstrap, a way to reach a port that machine listens on, and a state to
// report. An SSH client is one, and a WSL distribution is the other.
type Link interface {
	bootstrap.Conn
	Start(ctx context.Context) error
	Close() error
	Subscribe(fn func(remote.StatusEvent)) (cancel func())
	Forwards() Forwards
	// Dirs resolves dir (empty or ~ meaning the login home) on that machine and
	// lists what is inside it, in that machine's own spelling.
	Dirs(ctx context.Context, dir string) (at string, entries []sftpfs.Entry, err error)
}

// workspaceMapper is a Link whose machine spells folders its own way, so a path
// chosen on this one has to be translated before the kernel there is asked to
// open it.
type workspaceMapper interface {
	Workspace(ctx context.Context, p string) (string, error)
}

// Forwards is the part of a forward registry the pool drives.
type Forwards interface {
	Add(spec forward.Spec) (string, error)
	List() []forward.Entry
	Remove(name string) error
}

// sshLink adapts a supervised SSH client to Link.
type sshLink struct{ *remote.Client }

func (l sshLink) Forwards() Forwards { return l.Client.Forwards() }

func (l sshLink) Dirs(ctx context.Context, dir string) (string, []sftpfs.Entry, error) {
	vol, err := l.Volume()
	if err != nil {
		return "", nil, err
	}
	return sftpfs.Dirs(ctx, vol, dir)
}
