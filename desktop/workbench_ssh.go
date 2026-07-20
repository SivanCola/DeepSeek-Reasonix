package main

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"

	"golang.org/x/crypto/ssh"

	"reasonix/internal/config"
	"reasonix/internal/remote"
	"reasonix/internal/remote/workbench/transport"
)

// newWorkbenchSSHFactory returns a transport factory for the workbench path.
// Windows uses system OpenSSH + AskPass + Job Object; other platforms use Go SSH
// stdio running `reasonix remote attach-workspace --stdio`.
func newWorkbenchSSHFactory(entry config.RemoteHostEntry) (transport.Factory, error) {
	if runtime.GOOS == "windows" {
		return newWindowsWorkbenchSSHFactory(entry)
	}
	return transport.FactoryFunc(func(ctx context.Context) (transport.Stream, error) {
		return openGoSSHAttachWorkspace(ctx, entry)
	}), nil
}

func newWindowsWorkbenchSSHFactory(entry config.RemoteHostEntry) (transport.Factory, error) {
	factory := &RemoteSSHTransportFactory{
		AskPass: &RemoteAskPassBroker{},
	}
	hostEntry, err := mapConfigHostToWorkbenchEntry(entry)
	if err != nil {
		return nil, err
	}
	return NewRemoteSSHHostTransportFactory(factory, hostEntry)
}

func mapConfigHostToWorkbenchEntry(entry config.RemoteHostEntry) (RemoteHostEntry, error) {
	label := entry.Host
	if entry.User != "" {
		label = entry.User + "@" + entry.Host
	}
	port := entry.PortOrDefault()
	if entry.UseSSHConfig && entry.Host != "" && !strings.Contains(entry.Host, ".") && entry.User == "" {
		return NewRemoteHostEntry(entry.Host, label)
	}
	dest := entry.Host
	if entry.User != "" {
		dest = entry.User + "@" + entry.Host
	}
	return NewRemoteDirectHostEntry(dest, port, label)
}

// goSSHStream adapts a Go SSH session's stdio to transport.Stream.
type goSSHStream struct {
	client  *remote.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	cancel  context.CancelFunc
}

func (s *goSSHStream) Read(p []byte) (int, error)  { return s.stdout.Read(p) }
func (s *goSSHStream) Write(p []byte) (int, error) { return s.stdin.Write(p) }
func (s *goSSHStream) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.session != nil {
		_ = s.session.Close()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
	return nil
}

func openGoSSHAttachWorkspace(ctx context.Context, entry config.RemoteHostEntry) (transport.Stream, error) {
	resolved, err := remote.ResolveHost(nil, entry.Name, nil)
	if err != nil {
		// Ad-hoc from fields.
		target := entry.Host
		if entry.User != "" {
			target = entry.User + "@" + entry.Host
		}
		if entry.Port > 0 && entry.Port != 22 {
			target = fmt.Sprintf("%s:%d", target, entry.Port)
		}
		resolved, err = remote.ResolveHost(nil, target, nil)
		if err != nil {
			return nil, err
		}
	}
	if entry.IdentityFile != "" {
		resolved.IdentityFile = entry.IdentityFile
		resolved.IdentityFiles = []string{entry.IdentityFile}
	}
	opts := remote.Options{Host: resolved}
	c, err := remote.New(opts)
	if err != nil {
		return nil, err
	}
	if err := c.Start(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	cl, err := c.SSH()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	sess, err := cl.NewSession()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	cmd := "reasonix remote attach-workspace --stdio"
	if ws := strings.TrimSpace(entry.Workspace); ws != "" {
		cmd = "REASONIX_ATTACH_WORKSPACE=" + shellSingleQuote(ws) + " " + cmd
	}
	if err := sess.Start(cmd); err != nil {
		_ = c.Close()
		return nil, err
	}
	ctx2, cancel := context.WithCancel(ctx)
	go func() {
		<-ctx2.Done()
		_ = sess.Close()
	}()
	go func() { _ = sess.Wait(); cancel() }()
	return &goSSHStream{client: c, session: sess, stdin: stdin, stdout: stdout, cancel: cancel}, nil
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
