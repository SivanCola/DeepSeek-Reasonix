package bootstrap

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/platform/remote"
	"reasonix/internal/platform/remote/sftpfs"
	"reasonix/internal/platform/remote/sshtest"
)

func realClient(t *testing.T, opts sshtest.Options) *remote.Client {
	t.Helper()
	skipOnWindows(t)
	if err := exec.Command("stat", "-c", "%s", "/").Run(); err != nil {
		t.Skip("needs a GNU userland")
	}
	opts.Password = "pw"
	srv := sshtest.Start(t, opts)
	host, err := remote.ResolveHost(nil, "test@"+srv.Addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := remote.New(remote.Options{
		Host: host,
		Auth: remote.AuthOptions{DisableAgent: true, Password: func() (string, error) { return "pw", nil }},
		HostKeys: &remote.HostKeyPolicy{
			SystemKnownHosts: []string{filepath.Join(testenv.TempDir(t), "none")},
			ManagedPath:      filepath.Join(testenv.TempDir(t), "known_hosts"),
			Prompt:           func(context.Context, remote.HostKeyQuestion) (bool, error) { return true, nil },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// A server that refuses sftp is not a machine that cannot be probed: the
// report names the file route it found, so "connection OK" is not followed by
// a failure at the first folder.
func TestProbeNamesHowTheMachinesFilesAreReached(t *testing.T) {
	for name, tc := range map[string]struct {
		opts sshtest.Options
		want sftpfs.Mode
	}{
		"sftp served":  {sshtest.Options{Shell: true, SFTPRoot: testenv.TempDir(t)}, sftpfs.ModeSFTP},
		"sftp refused": {sshtest.Options{Shell: true}, sftpfs.ModeExec},
	} {
		t.Run(name, func(t *testing.T) {
			rep, err := Probe(context.Background(), realClient(t, tc.opts), Options{})
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if rep.Files != tc.want || rep.OS != "linux" {
				t.Fatalf("report = %+v, want files %q on linux", rep, tc.want)
			}
		})
	}
}
