package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/fileutil"
	"reasonix/internal/session"
)

type desktopMigrationRecord struct {
	SourceKey       string `json:"sourceKey"`
	TargetSessionID string `json:"targetSessionId"`
	ContentDigest   string `json:"contentDigest,omitempty"`
	Status          string `json:"status"`
	ErrorCode       string `json:"errorCode,omitempty"`
	Attempts        int    `json:"attempts"`
}

type desktopMigrationLedger struct {
	Version int                               `json:"version"`
	Records map[string]desktopMigrationRecord `json:"records"`
}

var desktopMigrationMu sync.Mutex

func desktopMigrationLedgerPath() string {
	return filepath.Join(desktopConfigDir(), "desktop", "session-migration-v5.json")
}

func (a *App) startDesktopSessionMigration(ctx context.Context) {
	if a == nil {
		return
	}
	go func() {
		if err := a.recoverDesktopPendingCreates(ctx); err != nil {
			slogWarnDesktopMigration(err)
		}
		if err := a.migrateDesktopSessionsV5(ctx); err != nil {
			slogWarnDesktopMigration(err)
		}
	}()
}

// recoverDesktopPendingCreates completes the registry half of a create that
// reached durable session publication before the process stopped. A missing
// target is safe to forget: no canonical content exists for the pending ID and
// the UI can retry creation without inventing a replacement identity.
func (a *App) recoverDesktopPendingCreates(ctx context.Context) error {
	state, err := a.workspaceRegistry().Load(ctx)
	if err != nil {
		return err
	}
	service := a.desktopSessionService("")
	var joined error
	for sessionID, pending := range state.PendingCreates {
		ref := session.SessionRef{HostID: localDesktopHostID, SessionID: sessionID}
		if _, err := service.Query().Snapshot(ctx, ref); err == nil {
			if attachErr := a.workspaceRegistry().AttachSession(ctx, pending.OperationID, pending.WorkspaceID, sessionID, ""); attachErr != nil {
				joined = errors.Join(joined, attachErr)
			} else {
				a.desktopSessions.pendingCreateRecovered.Add(1)
			}
		} else if errors.Is(err, session.ErrSessionNotFound) {
			if abortErr := a.workspaceRegistry().AbortCreate(ctx, sessionID); abortErr != nil {
				joined = errors.Join(joined, abortErr)
			}
		} else {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func slogWarnDesktopMigration(err error) {
	// Keep migration logs content- and path-free. Detailed per-source state is
	// available through the local ledger and UI health row.
	if err != nil {
		slog.Warn("desktop session migration incomplete")
	}
}

type desktopMigrationSource struct {
	root          string
	scope         string
	workspaceRoot string
	exact         map[string]bool
}

func (a *App) migrateDesktopSessionsV5(ctx context.Context) error {
	tabs := loadTabsFile()
	projects := loadProjectsFile()
	sources := map[string]*desktopMigrationSource{}
	add := func(scope, workspaceRoot, root string) *desktopMigrationSource {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "." || root == "" || sameDesktopPath(root, a.desktopSessions.root) {
			return nil
		}
		key := canonicalRuntimeRoot(root)
		if current := sources[key]; current != nil {
			return current
		}
		source := &desktopMigrationSource{root: root, scope: scope, workspaceRoot: workspaceRoot, exact: map[string]bool{}}
		sources[key] = source
		return source
	}
	add("global", "", config.SessionStoreDir())
	add("global", "", config.ProjectSessionStoreDir(globalWorkspaceRoot()))
	for _, project := range projects.Projects {
		add("project", project.Root, config.ProjectSessionStoreDir(project.Root))
	}
	for _, tab := range tabs.Tabs {
		if strings.TrimSpace(tab.SessionID) == "" {
			continue
		}
		root := config.ProjectSessionStoreDir(globalWorkspaceRoot())
		if tab.Scope == "project" {
			root = config.ProjectSessionStoreDir(tab.WorkspaceRoot)
		}
		if source := add(tab.Scope, tab.WorkspaceRoot, root); source != nil {
			source.exact[tab.SessionID] = true
		}
	}
	var joined error
	for _, source := range sources {
		if err := a.migrateCanonicalStore(ctx, *source); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	legacySources := map[string]desktopMigrationSource{}
	addLegacy := func(scope, workspaceRoot, dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "." || dir == "" {
			return
		}
		key := canonicalRuntimeRoot(dir)
		if _, ok := legacySources[key]; !ok {
			legacySources[key] = desktopMigrationSource{root: dir, scope: scope, workspaceRoot: workspaceRoot, exact: map[string]bool{}}
		}
	}
	addLegacy("global", "", config.SessionDir())
	addLegacy("global", "", desktopSessionDir(globalWorkspaceRoot()))
	for _, project := range projects.Projects {
		addLegacy("project", project.Root, desktopSessionDir(project.Root))
	}
	for _, tab := range tabs.Tabs {
		path := filepath.Clean(strings.TrimSpace(tab.SessionPath))
		if path == "." || path == "" {
			continue
		}
		dir := filepath.Dir(path)
		key := canonicalRuntimeRoot(dir)
		source, ok := legacySources[key]
		if !ok {
			source = desktopMigrationSource{root: dir, scope: tab.Scope, workspaceRoot: tab.WorkspaceRoot, exact: map[string]bool{}}
		}
		source.exact[path] = true
		legacySources[key] = source
	}
	for _, source := range legacySources {
		if err := a.migrateLegacyDirectory(ctx, source); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (a *App) migrateCanonicalStore(ctx context.Context, source desktopMigrationSource) (retErr error) {
	if _, err := os.Stat(source.root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	old, err := session.NewService("migration-source", session.NewFilesystemPersistence(source.root))
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, old.Shutdown(context.Background())) }()
	infos, err := listAllCanonicalSessionInfo(ctx, old.Query())
	if err != nil {
		return err
	}
	workspaceID, err := a.ensureDesktopWorkspace(ctx, source.scope, source.workspaceRoot)
	if err != nil {
		return err
	}
	var joined error
	for _, info := range infos {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		exact := source.exact[info.SessionID]
		if !exact && info.Turns == 0 && strings.TrimSpace(info.Title) == "" && strings.TrimSpace(info.Preview) == "" {
			continue
		}
		if err := a.migrateCanonicalSession(ctx, old, source, workspaceID, info.SessionID); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (a *App) migrateCanonicalSession(ctx context.Context, old *session.Service, source desktopMigrationSource, workspaceID, sessionID string) error {
	digest := sha256.Sum256([]byte(canonicalRuntimeRoot(source.root) + "\x00" + sessionID))
	key := hex.EncodeToString(digest[:])
	oldRef := session.SessionRef{HostID: "migration-source", SessionID: sessionID}
	contentDigest, err := canonicalMigrationDigest(ctx, old.Query(), oldRef)
	if err != nil {
		return err
	}
	target := a.desktopSessionService("")
	targetID, needsImport, err := resolveMigrationTarget(ctx, target.Query(), sessionID, key, contentDigest)
	if err != nil {
		_ = updateDesktopMigrationLedger(key, sessionID, "failed", "target_conflict", contentDigest)
		return err
	}
	if err := updateDesktopMigrationLedger(key, targetID, "pending", "", contentDigest); err != nil {
		return err
	}
	if needsImport {
		tmp, err := os.MkdirTemp("", "reasonix-session-v5-export-")
		if err != nil {
			return err
		}
		bundle := filepath.Join(tmp, "bundle")
		defer os.RemoveAll(tmp)
		if err := old.Export(ctx, oldRef, bundle); err != nil {
			_ = updateDesktopMigrationLedger(key, targetID, "failed", "export", contentDigest)
			return err
		}
		if _, err := target.ImportWithHeader(ctx, bundle, session.CreateOptions{
			SessionID: targetID, CWD: desktopWorkspaceRoot(source.scope, source.workspaceRoot), Origin: session.SessionOriginCanonicalImport,
		}); err != nil {
			_ = updateDesktopMigrationLedger(key, targetID, "failed", "import", contentDigest)
			return err
		}
	}
	if err := a.workspaceRegistry().AttachSession(ctx, "", workspaceID, targetID, ""); err != nil {
		_ = updateDesktopMigrationLedger(key, targetID, "failed", "registry", contentDigest)
		return err
	}
	return updateDesktopMigrationLedger(key, targetID, "completed", "", contentDigest)
}

func (a *App) migrateLegacyDirectory(ctx context.Context, source desktopMigrationSource) error {
	entries, err := os.ReadDir(source.root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	workspaceID, err := a.ensureDesktopWorkspace(ctx, source.scope, source.workspaceRoot)
	if err != nil {
		return err
	}
	var joined error
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(source.root, entry.Name())
		exact := source.exact[path]
		meta, hasMeta, metaErr := agent.LoadBranchMeta(path)
		if metaErr != nil && !exact {
			continue
		}
		if !exact && (!hasMeta || (meta.Turns == 0 && strings.TrimSpace(meta.Name) == "" && strings.TrimSpace(meta.TopicTitle) == "")) {
			continue
		}
		if err := a.migrateLegacySession(ctx, path, source, workspaceID); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (a *App) migrateLegacySession(ctx context.Context, path string, source desktopMigrationSource, workspaceID string) (retErr error) {
	digest := sha256.Sum256([]byte(canonicalRuntimeRoot(path)))
	key := hex.EncodeToString(digest[:])
	if err := updateDesktopMigrationLedger(key, "", "pending", ""); err != nil {
		return err
	}
	stageRoot, err := os.MkdirTemp("", "reasonix-legacy-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageRoot)
	stage, err := session.NewService("migration-stage", session.NewFilesystemPersistence(filepath.Join(stageRoot, "sessions-v4")))
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, stage.Shutdown(context.Background())) }()
	runtime, result, err := stage.ContinueImported(ctx, path, "")
	if err != nil {
		_ = updateDesktopMigrationLedger(key, "", "failed", "legacy_import")
		return err
	}
	bundle := filepath.Join(stageRoot, "bundle")
	if err := stage.Export(ctx, runtime.Ref(), bundle); err != nil {
		_ = updateDesktopMigrationLedger(key, result.TargetID, "failed", "export")
		return err
	}
	if err := stage.Close(ctx, runtime.Ref()); err != nil {
		return err
	}
	target := a.desktopSessionService("")
	contentDigest, err := canonicalMigrationDigest(ctx, stage.Query(), runtime.Ref())
	if err != nil {
		return err
	}
	targetID, needsImport, err := resolveMigrationTarget(ctx, target.Query(), result.TargetID, key, contentDigest)
	if err != nil {
		_ = updateDesktopMigrationLedger(key, result.TargetID, "failed", "target_conflict", contentDigest)
		return err
	}
	if err := updateDesktopMigrationLedger(key, targetID, "pending", "", contentDigest); err != nil {
		return err
	}
	if needsImport {
		if _, err := target.ImportWithHeader(ctx, bundle, session.CreateOptions{
			SessionID: targetID, CWD: desktopWorkspaceRoot(source.scope, source.workspaceRoot), Origin: session.SessionOriginLegacyImport,
		}); err != nil {
			_ = updateDesktopMigrationLedger(key, targetID, "failed", "import", contentDigest)
			return err
		}
	}
	if err := a.workspaceRegistry().AttachSession(ctx, "", workspaceID, targetID, ""); err != nil {
		_ = updateDesktopMigrationLedger(key, targetID, "failed", "registry", contentDigest)
		return err
	}
	return updateDesktopMigrationLedger(key, targetID, "completed", "", contentDigest)
}

func canonicalMigrationDigest(ctx context.Context, query *session.Query, ref session.SessionRef) (string, error) {
	messages, err := query.History(ctx, ref)
	if err != nil {
		return "", err
	}
	return agent.ContentDigestForMessages(messages)
}

func resolveMigrationTarget(ctx context.Context, query *session.Query, preferredID, sourceKey, contentDigest string) (string, bool, error) {
	check := func(sessionID string) (bool, error) {
		digest, err := canonicalMigrationDigest(ctx, query, session.SessionRef{HostID: localDesktopHostID, SessionID: sessionID})
		if errors.Is(err, session.ErrSessionNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return digest == contentDigest, nil
	}
	if identical, err := check(preferredID); err != nil {
		return "", false, err
	} else if identical {
		return preferredID, false, nil
	} else if _, err := query.Snapshot(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: preferredID}); errors.Is(err, session.ErrSessionNotFound) {
		return preferredID, true, nil
	} else if err != nil {
		return "", false, err
	}
	digest := sha256.Sum256([]byte(sourceKey + "\x00" + contentDigest))
	conflictID := "migr-" + hex.EncodeToString(digest[:12])
	if identical, err := check(conflictID); err != nil {
		return "", false, err
	} else if identical {
		return conflictID, false, nil
	} else if _, err := query.Snapshot(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: conflictID}); errors.Is(err, session.ErrSessionNotFound) {
		return conflictID, true, nil
	} else if err != nil {
		return "", false, err
	}
	return "", false, errors.New("migration target identity collision")
}

func updateDesktopMigrationLedger(sourceKey, targetID, status, errorCode string, digest ...string) error {
	desktopMigrationMu.Lock()
	defer desktopMigrationMu.Unlock()
	path := desktopMigrationLedgerPath()
	ledger := desktopMigrationLedger{Version: 1, Records: map[string]desktopMigrationRecord{}}
	if body, err := os.ReadFile(path); err == nil {
		if unmarshalErr := json.Unmarshal(body, &ledger); unmarshalErr != nil {
			return unmarshalErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if ledger.Version > 1 {
		return fmt.Errorf("desktop migration ledger version %d is unsupported", ledger.Version)
	}
	if ledger.Records == nil {
		ledger.Records = map[string]desktopMigrationRecord{}
	}
	record := ledger.Records[sourceKey]
	record.SourceKey, record.TargetSessionID, record.Status, record.ErrorCode = sourceKey, targetID, status, errorCode
	if len(digest) > 0 {
		record.ContentDigest = digest[0]
	}
	if status == "pending" {
		record.Attempts++
	}
	ledger.Records[sourceKey] = record
	body, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(path, append(body, '\n'), 0o600)
}
