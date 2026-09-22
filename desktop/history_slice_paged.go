package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
)

func (a *App) pagedColdHistorySlice(ctx context.Context, sessionDir, path string, req HistorySliceRequest, heads ...string) (HistorySlice, bool, error) {
	ctx = a.historyMaintenance.Context(ctx)
	cacheRoot := config.CacheDir()
	if cacheRoot == "" {
		return HistorySlice{}, false, nil
	}
	head := ""
	if len(heads) > 0 {
		head = heads[0]
	}
	key := sha256.Sum256([]byte(sessionRuntimeKey(path) + "\x00" + head))
	cachePath := filepath.Join(cacheRoot, "history-display-v1", fmt.Sprintf("%x.sqlite", key))
	release, err := a.historyMaintenance.Foreground(ctx)
	if err != nil {
		return emptyHistorySlice(), true, err
	}
	pager, err := agent.OpenDisplayPager(ctx, path, cachePath, head)
	release()
	if err != nil {
		if errors.Is(err, agent.ErrDisplaySourceChanged) {
			return emptyHistorySlice(), true, err
		}
		if errors.Is(err, agent.ErrSessionDisplayReadModelDamaged) {
			return emptyHistorySlice(), true, err
		}
		if ctx.Err() != nil {
			return emptyHistorySlice(), true, ctx.Err()
		}
		return HistorySlice{}, false, nil
	}
	defer pager.Close()
	return a.historySliceFromPager(ctx, pager, sessionDir, path, req, fmt.Sprintf("%x", key))
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
