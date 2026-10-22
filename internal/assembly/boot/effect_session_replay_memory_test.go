package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/contract/config"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/state/history"
	"reasonix/internal/state/historycatalog"
	"reasonix/internal/state/store"
)

func writeBootLegacyReplayFixture(t *testing.T, path string, rewind bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	emit := func(record map[string]any) {
		t.Helper()
		record["schema_version"] = 2
		record["at"] = "2026-10-21T15:00:00Z"
		if err := enc.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	message := func(id, parent, content string) {
		emit(map[string]any{"type": "message", "id": id, "parent": parent, "head": "main", "msgs": []any{map[string]any{"role": "user", "content": content}}})
	}
	message("root", "", "surviving catalog decision")
	body := strings.Repeat("x", 1<<20)
	parent := "root"
	for i := range 65 {
		id := fmt.Sprintf("m%d", i)
		message(id, parent, body)
		parent = id
	}
	if rewind {
		emit(map[string]any{"type": "rewind", "head": "main", "to": "root"})
	}
}

func TestEffectHistoryIndexRefusesOversizedReplayAndKeepsOtherSources(t *testing.T) {
	home := isolateConfigHome(t)
	t.Setenv("REASONIX_HOME", home)
	sessions := config.RootsForHome(home).SessionDir()
	writeBootLegacyReplayFixture(t, filepath.Join(sessions, "20261021-150000.000000000-deepseek-flash.jsonl"), false)
	writeBootLegacyReplayFixture(t, filepath.Join(sessions, "20261021-150001.000000000-deepseek-flash.jsonl"), true)

	ready := make(chan historycatalog.Status, 1)
	var once sync.Once
	history.RegisterCatalogObserver(func(status historycatalog.Status, _ []string, _ string) {
		if status.Failed == 1 && status.Indexed >= 1 {
			once.Do(func() { ready <- status })
		}
	})
	const kind = "session-replay-memory-effect"
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return &effectRecordingProvider{}, nil })
	ws := statedHomeWorkspace(t, kind, "REASONIX_REPLAY_MEMORY_KEY")
	ctrl, err := Build(context.Background(), Options{Sink: event.Discard, Home: home, WorkspaceRoot: ws})
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	wait := 5 * time.Minute
	if deadline, ok := t.Deadline(); ok {
		wait = time.Until(deadline) / 2
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case status := <-ready:
		if status.Total != 2 {
			t.Fatalf("catalog indexed unexpected sources: %+v", status)
		}
	case <-timer.C:
		t.Fatal("background indexing did not isolate the oversized source and finish the rewound source")
	}
}
