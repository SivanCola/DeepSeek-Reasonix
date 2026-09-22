package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

func (a *App) pagedColdHistorySlice(ctx context.Context, sessionDir, path string, req HistorySliceRequest, heads ...string) (HistorySlice, bool, error) {
	head := ""
	if len(heads) > 0 {
		head = heads[0]
	}
	generation, err := nativeHistorySourceGeneration(path)
	if err != nil {
		return emptyHistorySlice(), true, err
	}
	sourceKey := sessionRuntimeKey(path)
	a.historyReaders.mu.Lock()
	if a.historyReaders.closed || a.shuttingDown.Load() {
		a.historyReaders.mu.Unlock()
		return emptyHistorySlice(), true, context.Canceled
	}
	job, release := a.acquireNativeHistoryLocked(path, head, sourceKey, generation)
	a.historyReaders.mu.Unlock()
	defer release()
	// Compatibility RPCs borrow the same preparation and cache owner as bound
	// readers. Neither path can rebuild underneath the other's live database.
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(job.ctx, cancel)
	defer func() { stop(); cancel() }()
	pager, err := job.wait(ctx)
	if err != nil {
		if errors.Is(err, agent.ErrDisplaySourceChanged) {
			return emptyHistorySlice(), true, err
		}
		if errors.Is(err, agent.ErrSessionDisplayReadModelDamaged) {
			return emptyHistorySlice(), true, err
		}
		if ctx.Err() != nil || job.ctx.Err() != nil {
			return emptyHistorySlice(), true, context.Canceled
		}
		return HistorySlice{}, false, nil
	}
	page, ready, err := a.historySliceFromPager(ctx, pager, sessionDir, path, req, job.key)
	if ctx.Err() != nil || job.ctx.Err() != nil {
		return emptyHistorySlice(), true, context.Canceled
	}
	return page, ready, err
}

func (a *App) historySliceFromPager(ctx context.Context, pager *agent.DisplayPager, sessionDir, path string, req HistorySliceRequest, sourceID string) (HistorySlice, bool, error) {
	pager = pager.WithContext(ctx)
	idx := &pager.Header
	src := &historySliceSource{sessionID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), total: idx.MessageCount,
		totalTurns: idx.AuthoredTurns, revision: idx.Revision, revKnown: idx.RevisionKnown, digest: idx.ContentDigest,
		position: pager.Entry, usersBefore: pager.UsersBefore, maxFetch: 500, windowOnly: true}
	src.sourceID = sourceID
	if idx.RevisionKnown {
		src.epoch = int(idx.Revision)
	} else {
		src.revision = 0
	}
	src.fetch = func(lo, hi int) ([]provider.Message, error) {
		if err := pager.Validate(); err != nil {
			return nil, err
		}
		if pager.DAG {
			return pager.DAGMessages(lo, hi)
		}
		entries, err := pager.Entries(lo, hi)
		if err != nil {
			return nil, err
		}
		messages, err := readSessionMessagesAtOffsetsContext(ctx, path, entries)
		if err != nil {
			return nil, err
		}
		return messages, pager.Validate()
	}
	src.windowBytes = func(lo, hi int) int64 {
		if hi <= lo {
			return 0
		}
		if pager.DAG {
			entries, err := pager.Entries(lo, hi)
			if err != nil {
				src.readErr = err
				return 0
			}
			var total int64
			for _, entry := range entries {
				total += entry.Length
			}
			return total
		}
		first, err := pager.Entry(lo)
		if err != nil {
			src.readErr = err
			return 0
		}
		last, err := pager.Entry(hi - 1)
		if err != nil {
			src.readErr = err
			return 0
		}
		return last.Offset + last.Length - first.Offset
	}
	page, err := a.pageHistorySliceSource(src, req, sessionDisplayResolver(sessionDir, path), sessionPlannerDisplayTurns(sessionDir, path), nil, path)
	if src.readErr != nil {
		return emptyHistorySlice(), true, src.readErr
	}
	page.Source = "index"
	if pager.Built {
		page.Source = "scan"
	}
	if pager.DAG {
		page.Source = "event-log"
	}
	return page, true, err
}
