package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"reasonix/desktop/internal/workspacestate"
	"reasonix/internal/agent"
	"reasonix/internal/session"
)

const localDesktopHostID = "local"

type WorkspaceSummary struct {
	ID         string   `json:"id"`
	Root       string   `json:"root"`
	Title      string   `json:"title"`
	SessionIDs []string `json:"sessionIds"`
	Visible    bool     `json:"visible"`
	CreatedAt  int64    `json:"createdAt"`
	UpdatedAt  int64    `json:"updatedAt"`
}

type WorkspacePendingCreate struct {
	OperationID string `json:"operationId"`
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	CreatedAt   int64  `json:"createdAt"`
}

type WorkspaceSnapshot struct {
	Generation         uint64                   `json:"generation"`
	Workspaces         []WorkspaceSummary       `json:"workspaces"`
	ArchivedSessionIDs []string                 `json:"archivedSessionIds"`
	PendingCreates     []WorkspacePendingCreate `json:"pendingCreates"`
}

type WorkspaceSessionSummary struct {
	Ref             session.SessionRef `json:"ref"`
	WorkspaceID     string             `json:"workspaceId"`
	Title           string             `json:"title"`
	Preview         string             `json:"preview"`
	Turns           int                `json:"turns"`
	CreatedAt       int64              `json:"createdAt"`
	UpdatedAt       int64              `json:"updatedAt"`
	ModelRef        string             `json:"modelRef,omitempty"`
	ParentSessionID string             `json:"parentSessionId,omitempty"`
	Blank           bool               `json:"blank"`
	Archived        bool               `json:"archived"`
	Running         bool               `json:"running"`
	MetadataStatus  string             `json:"metadataStatus"`
	Health          string             `json:"health"`
}

type WorkspaceSessionPage struct {
	Sessions           []WorkspaceSessionSummary `json:"sessions"`
	NextCursor         string                    `json:"nextCursor,omitempty"`
	RegistryGeneration uint64                    `json:"registryGeneration"`
}

func unixMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

func (a *App) GetWorkspaceSnapshot() (WorkspaceSnapshot, error) {
	state, err := a.workspaceRegistry().Load(context.Background())
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	result := WorkspaceSnapshot{
		Generation:         state.Generation,
		Workspaces:         make([]WorkspaceSummary, 0, len(state.WorkspaceIDs)),
		ArchivedSessionIDs: append([]string(nil), state.ArchivedSessionIDs...),
		PendingCreates:     make([]WorkspacePendingCreate, 0, len(state.PendingCreates)),
	}
	for _, id := range state.WorkspaceIDs {
		workspace, ok := state.Workspaces[id]
		if !ok {
			continue
		}
		result.Workspaces = append(result.Workspaces, WorkspaceSummary{
			ID: workspace.ID, Root: workspace.Root, Title: workspace.Title,
			SessionIDs: append([]string(nil), workspace.SessionIDs...), Visible: workspace.Visible,
			CreatedAt: unixMillis(workspace.CreatedAt), UpdatedAt: unixMillis(workspace.UpdatedAt),
		})
	}
	for _, pending := range state.PendingCreates {
		result.PendingCreates = append(result.PendingCreates, WorkspacePendingCreate{
			OperationID: pending.OperationID, WorkspaceID: pending.WorkspaceID,
			SessionID: pending.SessionID, CreatedAt: unixMillis(pending.CreatedAt),
		})
	}
	return result, nil
}

func (a *App) ListWorkspaceSessions(workspaceID, queryText, cursor string, limit int, includeArchived bool) (WorkspaceSessionPage, error) {
	state, err := a.workspaceRegistry().Load(context.Background())
	if err != nil {
		return WorkspaceSessionPage{}, err
	}
	workspace, ok := state.Workspaces[strings.TrimSpace(workspaceID)]
	if !ok {
		return WorkspaceSessionPage{}, workspacestate.ErrWorkspaceNotFound
	}
	start, err := decodeWorkspaceSessionCursor(cursor, state.Generation)
	if err != nil {
		return WorkspaceSessionPage{}, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	service := a.desktopSessionService("")
	infos, listErr := listAllCanonicalSessionInfo(context.Background(), service.Query())
	archived := make(map[string]bool, len(state.ArchivedSessionIDs))
	for _, id := range state.ArchivedSessionIDs {
		archived[id] = true
	}
	needle := strings.ToLower(strings.TrimSpace(queryText))
	rows := make([]WorkspaceSessionSummary, 0, len(workspace.SessionIDs))
	for _, sessionID := range workspace.SessionIDs {
		isArchived := archived[sessionID]
		if isArchived && !includeArchived {
			continue
		}
		info, found := infos[sessionID]
		row := workspaceSessionRow(workspace.ID, sessionID, info, found, isArchived, service)
		if needle != "" && !strings.Contains(strings.ToLower(row.Title+"\n"+row.Preview+"\n"+sessionID), needle) {
			continue
		}
		rows = append(rows, row)
	}
	if start > len(rows) {
		start = len(rows)
	}
	end := start + limit
	if end > len(rows) {
		end = len(rows)
	}
	page := WorkspaceSessionPage{
		Sessions:           append([]WorkspaceSessionSummary(nil), rows[start:end]...),
		RegistryGeneration: state.Generation,
	}
	if end < len(rows) {
		page.NextCursor = fmt.Sprintf("%d:%d", state.Generation, end)
	}
	if listErr != nil && len(page.Sessions) == 0 {
		return page, listErr
	}
	return page, nil
}

func listAllCanonicalSessionInfo(ctx context.Context, query *session.Query) (map[string]session.SessionInfo, error) {
	infos := map[string]session.SessionInfo{}
	if query == nil {
		return infos, errors.New("desktop canonical session query is unavailable")
	}
	var cursor string
	for {
		page, err := query.List(ctx, cursor, 100)
		if err != nil {
			return infos, err
		}
		for _, info := range page.Sessions {
			infos[info.SessionID] = info
		}
		if page.NextCursor == "" {
			return infos, nil
		}
		if page.NextCursor == cursor {
			return infos, errors.New("desktop canonical session cursor did not advance")
		}
		cursor = page.NextCursor
	}
}

func workspaceSessionRow(workspaceID, sessionID string, info session.SessionInfo, found, archived bool, service *session.Service) WorkspaceSessionSummary {
	ref := session.SessionRef{HostID: localDesktopHostID, SessionID: sessionID}
	row := WorkspaceSessionSummary{
		Ref: ref, WorkspaceID: workspaceID, Archived: archived,
		Blank: true, MetadataStatus: "indexing", Health: "migrating",
	}
	if found {
		row.Title, row.Preview, row.Turns = info.Title, info.Preview, info.Turns
		row.CreatedAt, row.UpdatedAt = unixMillis(info.CreatedAt), unixMillis(info.UpdatedAt)
		row.ModelRef, row.ParentSessionID = info.ModelRef, info.ParentSessionID
		row.Blank = info.Turns == 0 && strings.TrimSpace(info.Preview) == ""
		row.MetadataStatus = info.MetadataStatus
		row.Health = "healthy"
		if info.Error != "" {
			row.MetadataStatus, row.Health = "failed", "read_only"
		}
	}
	if service != nil {
		_, row.Running = service.Runtime(ref)
	}
	return row
}

func decodeWorkspaceSessionCursor(cursor string, generation uint64) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 2 {
		return 0, errors.New("invalid workspace session cursor")
	}
	wantGeneration, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || wantGeneration != generation {
		return 0, errors.New("workspace session cursor is stale")
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 {
		return 0, errors.New("invalid workspace session cursor")
	}
	return offset, nil
}

func validateLocalSessionRef(ref session.SessionRef) error {
	if ref.HostID != localDesktopHostID || strings.TrimSpace(ref.SessionID) == "" {
		return errors.New("a local canonical session reference is required")
	}
	return nil
}

func (a *App) ArchiveCanonicalSession(ref session.SessionRef) error {
	if err := validateLocalSessionRef(ref); err != nil {
		return err
	}
	if err := a.workspaceRegistry().ArchiveSession(context.Background(), ref.SessionID); err != nil {
		return err
	}
	a.emitProjectTreeChanged()
	return nil
}

func (a *App) RestoreCanonicalSession(ref session.SessionRef) error {
	if err := validateLocalSessionRef(ref); err != nil {
		return err
	}
	if err := a.workspaceRegistry().RestoreSession(context.Background(), ref.SessionID); err != nil {
		return err
	}
	a.emitProjectTreeChanged()
	return nil
}

func (a *App) MoveWorkspaceSession(workspaceID, sessionID, beforeSessionID string) error {
	if err := a.workspaceRegistry().MoveSession(context.Background(), workspaceID, sessionID, beforeSessionID); err != nil {
		return err
	}
	a.emitProjectTreeChanged()
	return nil
}

func (a *App) ReadSessionHistory(ref session.SessionRef, cursor string, limit int) (HistoryPage, error) {
	if err := validateLocalSessionRef(ref); err != nil {
		return HistoryPage{}, err
	}
	beforeTurn := 0
	if strings.TrimSpace(cursor) != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 {
			return HistoryPage{}, errors.New("invalid session history cursor")
		}
		beforeTurn = parsed
	}
	messages, err := a.desktopSessionService("").Query().History(a.bootContext(), ref)
	if err != nil {
		return HistoryPage{}, err
	}
	page := historyPageFromProviderMessages(messages, func(content string) string { return content }, nil, nil, beforeTurn, limit)
	digest, _ := agent.ContentDigestForMessages(messages)
	return historyPageWithFingerprint(page, sessionRoute(ref.SessionID), digest), nil
}

// OpenSession installs exactly ref into the current local surface. It first
// proves the target history is readable; a missing or damaged identity never
// creates an empty replacement and never clears the currently visible log.
func (a *App) OpenSession(ref session.SessionRef) (HistoryPage, error) {
	page, err := a.ReadSessionHistory(ref, "", defaultHistoryPageTurns)
	if err != nil {
		return HistoryPage{}, err
	}
	tab, ctrl := a.tabAndCtrlByID("")
	if tab == nil || ctrl == nil {
		return HistoryPage{}, errors.New("workspace is not ready")
	}
	if _, err := a.resumeCanonicalSessionForTranscript(tab, ctrl, sessionRoute(ref.SessionID), defaultHistoryPageTurns, false); err != nil {
		return HistoryPage{}, err
	}
	return page, nil
}

func (a *App) RenameCanonicalSession(ref session.SessionRef, title string) error {
	if err := validateLocalSessionRef(ref); err != nil {
		return err
	}
	contained, err := a.workspaceRegistry().Contains(a.bootContext(), ref.SessionID)
	if err != nil {
		return err
	}
	if !contained {
		return workspacestate.ErrSessionNotFound
	}
	if err := a.desktopSessionService("").SetTitle(a.bootContext(), ref, strings.TrimSpace(title)); err != nil {
		return err
	}
	a.emitProjectTreeChanged()
	return nil
}
