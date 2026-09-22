package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"reasonix/internal/history"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/taskcatalog"
)

func (a *App) runSessionCatalog(ctx context.Context, initialReconcileDone chan struct{}, metadataRequests <-chan struct{}) {
	initialReconcileFinished := false
	defer func() {
		if !initialReconcileFinished {
			close(initialReconcileDone)
		}
	}()
	path := sessioncatalog.DefaultPath()
	freshGeneration := false
	if strings.TrimSpace(path) != "" {
		_, statErr := os.Stat(path)
		freshGeneration = errors.Is(statErr, os.ErrNotExist)
	}
	targets := a.sessionCatalogTargets()
	history.RegisterCatalogRoots(historyCatalogRoots(targets))
	projects := loadProjectsFile()
	taskcatalog.RegisterSharedProject(globalWorkspaceRoot(), projects.GlobalTitle)
	for _, project := range projects.Projects {
		taskcatalog.RegisterSharedProject(project.Root, projectDisplayName(project))
	}
	catalog, err := sessioncatalog.Open(ctx, sessioncatalog.Options{
		Path:         path,
		MetadataOnly: true,
		StartPaused:  true,
		Maintenance:  &a.historyMaintenance,
		OnRevision: func(revision uint64, roots []string, reason string) {
			a.emitProjectTreeChangedV2(revision, roots, reason)
		},
	})
	if err != nil {
		slog.Warn("desktop: open session catalog", "err", err)
		return
	}
	if ctx.Err() != nil || a.shuttingDown.Load() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_ = catalog.Close(closeCtx)
		closeCancel()
		return
	}
	a.sessionCatalog.Store(catalog)
	if freshGeneration {
		catalog.MarkRepairReason("generation_upgrade")
	}
	// Watch immediately, including while restored identities are pending. The
	// watcher publishes admission without waiting for metadata synchronization.
	a.watchSessionCatalog(ctx, catalog, metadataRequests, func() {
		close(initialReconcileDone)
		initialReconcileFinished = true
	})
}
