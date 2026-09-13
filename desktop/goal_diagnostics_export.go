package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"reasonix/desktop/internal/hostrpc"
	"reasonix/internal/control"
	"reasonix/internal/servecontract"
)

// ExportGoalDiagnostics lets the user save the authoritative v3 event stream,
// including tool calls/results and the exact goal/runtime identity, without
// relying on the frontend's paginated transcript.
func (a *App) ExportGoalDiagnostics() (string, error) {
	tab, api := a.activeTabAndCtrl()
	if tab == nil {
		return "", errors.New("goal diagnostics are unavailable for this session")
	}
	path, err := a.nativeHost().SaveFileDialog(a.ctx, nativeDialogOptions{
		Title:                "Export goal diagnostics",
		DefaultDirectory:     dialogDefaultDirectory(tab.WorkspaceRoot),
		DefaultFilename:      safeExportFilename(tab.TopicTitle + "-goal-diagnostics.json"),
		CanCreateDirectories: true,
		Filters:              exportFileFilters("application/json", ".json"),
	})
	if err != nil || path == "" {
		return "", err
	}
	if filepath.Ext(path) == "" {
		path += ".json"
	}
	var payload []byte
	ctrl, local := api.(*control.Controller)
	if local && ctrl != nil {
		payload, err = ctrl.ExportGoalDiagnostics(a.ctx, control.GoalDiagnosticMetadata{
			ApplicationVersion: version,
			BuildCommit:        buildCommit(),
			ProtocolVersion:    hostrpc.ProtocolVersion,
			Capabilities:       []string{"session-events-v3", "session-identity-v1", servecontract.GoalLifecycleV2},
		})
	} else if a.isRemoteTab(tab.ID) {
		payload, err = a.exportRemoteGoalDiagnostics(tab.ID)
	} else {
		err = errors.New("goal diagnostics are unavailable for this session")
	}
	if err != nil {
		return "", err
	}
	if err := a.SaveExportFile(path, string(payload), false); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) exportRemoteGoalDiagnostics(tabID string) ([]byte, error) {
	if err := a.requireRemoteGoalLifecycle(tabID); err != nil {
		return nil, err
	}
	client, base, expectedPath, err := a.remoteTabCommandTarget(tabID)
	if err != nil {
		return nil, err
	}
	resp, err := serveDoForSession(a.ctx, client, http.MethodGet, serveURL(base, "/goal-diagnostics"), nil, expectedPath)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		return nil, fmt.Errorf("export remote goal diagnostics: status %d: %s", resp.StatusCode, string(data))
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return nil, fmt.Errorf("read remote goal diagnostics: %w", err)
	}
	return payload, nil
}
