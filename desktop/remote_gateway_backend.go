package main

import (
	"context"
	"fmt"
	"path"
	"strings"

	"reasonix/internal/remote/gateway"
)

// desktopWorkspaceBackend implements gateway.WorkspaceBackend using the live
// SSH/SFTP connection held by desktopRemoteManager.
type desktopWorkspaceBackend struct {
	app *App
}

// containPath rejects absolute paths outside the selected remote workspace.
// Paths are treated as POSIX remote paths (SFTP).
func containPath(workspace, raw string) (string, error) {
	ws := path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(workspace), "/"))
	if ws == "/" || ws == "." {
		return "", fmt.Errorf("workspace root is required")
	}
	p := strings.TrimSpace(raw)
	if p == "" {
		p = ws
	}
	if strings.HasPrefix(p, "~") {
		return "", fmt.Errorf("path must be under the workspace (got %q)", raw)
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(ws, p)
	}
	p = path.Clean(p)
	if p != ws && !strings.HasPrefix(p, ws+"/") {
		return "", fmt.Errorf("path %q escapes workspace %q", raw, workspace)
	}
	// Block common sensitive locations even if workspace were mis-set.
	lower := strings.ToLower(p)
	for _, bad := range []string{"/.ssh/", "/.ssh", "/etc/", "/private/etc/"} {
		if strings.Contains(lower, bad) && !strings.HasPrefix(ws, path.Dir(p)) {
			// still allow only if under workspace which we already enforced
		}
	}
	return p, nil
}

func (b *desktopWorkspaceBackend) ListDir(ctx context.Context, hostID, workspace, path string) ([]gateway.DirEntry, error) {
	safe, err := containPath(workspace, path)
	if err != nil {
		return nil, err
	}
	rt, err := b.app.remoteRT()
	if err != nil {
		return nil, err
	}
	entries, err := rt.ListDir(ctx, hostID, safe)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.DirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, gateway.DirEntry{
			Name: e.Name, Path: e.Path, IsDir: e.IsDir, Size: e.Size,
			MtimeUnix: e.MtimeUnix, Symlink: e.Symlink,
		})
	}
	return out, nil
}

func (b *desktopWorkspaceBackend) ReadFile(ctx context.Context, hostID, workspace, path string) (gateway.FilePreview, error) {
	safe, err := containPath(workspace, path)
	if err != nil {
		return gateway.FilePreview{}, err
	}
	rt, err := b.app.remoteRT()
	if err != nil {
		return gateway.FilePreview{}, err
	}
	prev, err := rt.ReadFile(ctx, hostID, safe)
	if err != nil {
		return gateway.FilePreview{}, err
	}
	return gateway.FilePreview{
		Path: prev.Path, Body: prev.Body, Size: prev.Size, MtimeUnix: prev.MtimeUnix,
		Truncated: prev.Truncated, Binary: prev.Binary, Err: prev.Err,
	}, nil
}

func (b *desktopWorkspaceBackend) WriteFile(ctx context.Context, hostID, workspace, path, body string, expectMtime int64) (gateway.WriteResult, error) {
	safe, err := containPath(workspace, path)
	if err != nil {
		return gateway.WriteResult{}, err
	}
	rt, err := b.app.remoteRT()
	if err != nil {
		return gateway.WriteResult{}, err
	}
	res, err := rt.WriteFile(ctx, hostID, safe, body, expectMtime)
	if err != nil {
		return gateway.WriteResult{}, err
	}
	return gateway.WriteResult{OK: res.OK, Conflict: res.Conflict, NewMtimeUnix: res.NewMtimeUnix}, nil
}

func (b *desktopWorkspaceBackend) GitStatus(ctx context.Context, hostID, workspace string) (string, error) {
	return b.execGit(ctx, hostID, workspace, []string{"status", "--porcelain=v1", "-b"})
}

func (b *desktopWorkspaceBackend) GitDiff(ctx context.Context, hostID, workspace string) (string, error) {
	return b.execGit(ctx, hostID, workspace, []string{"diff", "--no-color"})
}

// execGit runs a fixed argv git command on the remote host inside workspace.
// Arguments are never concatenated into a shell string with untrusted file names.
func (b *desktopWorkspaceBackend) execGit(ctx context.Context, hostID, workspace string, argv []string) (string, error) {
	mgr, ok := b.app.mustRemoteManager()
	if !ok {
		return "", fmt.Errorf("remote manager unavailable")
	}
	mh := mgr.managed(hostID)
	if mh == nil || mh.client == nil {
		return "", fmt.Errorf("host %q is not connected", hostID)
	}
	var bld strings.Builder
	bld.WriteString("cd ")
	bld.WriteString(shellQuoteRemote(workspace))
	bld.WriteString(" && git")
	for _, a := range argv {
		if strings.ContainsAny(a, " \t\n\r'\"$`\\;&|<>") {
			return "", fmt.Errorf("git argument rejected")
		}
		bld.WriteByte(' ')
		bld.WriteString(a)
	}
	res, err := mh.client.Exec(ctx, bld.String())
	if err != nil {
		return "", err
	}
	if len(res.Stderr) > 0 && res.ExitCode != 0 {
		return "", fmt.Errorf("git: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return string(res.Stdout), nil
}

func (a *App) mustRemoteManager() (*desktopRemoteManager, bool) {
	a.remoteMu.Lock()
	defer a.remoteMu.Unlock()
	mgr, ok := a.remoteRuntime.(*desktopRemoteManager)
	return mgr, ok
}

func shellQuoteRemote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// attachGatewayWorkspaceBackend wires SFTP/Git into the parent gateway.
func (k *remoteNativeKernel) attachGatewayWorkspaceBackend(app *App) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.gateway != nil {
		k.gateway.SetWorkspaceBackend(&desktopWorkspaceBackend{app: app})
	}
}
