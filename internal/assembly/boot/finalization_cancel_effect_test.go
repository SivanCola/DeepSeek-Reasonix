package boot

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/base/filelock"
	"reasonix/internal/base/workspaceid"
	"reasonix/internal/contract/config"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/session/control"
	"reasonix/internal/tools/builtin"
)

type finalizationEffectSink struct {
	bundleAuditSink
	report  chan struct{}
	release chan struct{}
	done    chan event.Event
	once    sync.Once
}

func (s *finalizationEffectSink) Emit(e event.Event) {
	if e.Kind == event.TurnDone {
		s.done <- e
	}
}

func (s *finalizationEffectSink) RecordCompletionReport(event.CompletionReportAudit) {
	s.once.Do(func() {
		close(s.report)
		<-s.release
	})
}

func TestEffectCancelAfterFinalAnswerReleasesTurnDespiteHeldEvidenceLock(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	p := &effectRecordingProvider{}
	kind := "boot-finalization-cancel-" + workspaceid.PathFingerprint(dir)
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return p, nil })
	writeFile(t, dir, "reasonix.toml", `default_model = "fixture"
[[providers]]
name = "fixture"
kind = "`+kind+`"
model = "fixture"
`)
	approveWorkspace(t, dir)
	sink := &finalizationEffectSink{report: make(chan struct{}), release: make(chan struct{}), done: make(chan event.Event, 2)}
	ctrl, err := Build(t.Context(), Options{WorkspaceRoot: dir, Sink: sink, HeadlessApprovalMode: control.ToolApprovalAuto, resolvedShell: pinnedEffectShell()})
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	lock := filepath.Join(config.MemoryUserDir(), builtin.TrustedStateDir, "streams", workspaceid.PathFingerprint(dir), "LOCK")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := filelock.Acquire(t.Context(), lock)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctrl.EnsureSessionPath()
	ctrl.Send("Say hello.")
	select {
	case <-sink.report:
	case <-time.After(10 * time.Second):
		close(sink.release)
		t.Fatal("completion report was not reached")
	}
	defer close(sink.release)
	if !ctrl.TurnInFlight() || len(ctrl.Jobs()) != 0 || len(p.requests()) != 1 {
		t.Fatalf("finalization state: running=%t jobs=%d requests=%d", ctrl.TurnInFlight(), len(ctrl.Jobs()), len(p.requests()))
	}
	history := ctrl.History()
	if len(history) == 0 || history[len(history)-1].Role != provider.RoleAssistant || history[len(history)-1].Content == "" {
		t.Fatal("final answer was not committed before finalization")
	}
	ctrl.Cancel()
	sink.release <- struct{}{}
	select {
	case done := <-sink.done:
		if !done.Cancelled || ctrl.TurnInFlight() {
			t.Fatalf("completion = %+v, running=%t", done, ctrl.TurnInFlight())
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled turn still waited on the evidence lock")
	}
	if len(p.requests()) != 1 || len(ctrl.Jobs()) != 0 {
		t.Fatal("finishing the turn started more model requests or jobs")
	}
	for _, audit := range sink.audits() {
		if audit.Sealed || audit.FailureCode == "" {
			t.Fatalf("cancelled seal must report its gap: %+v", audit)
		}
	}
	history = ctrl.History()
	if len(history) == 0 || history[len(history)-1].Role != provider.RoleAssistant || history[len(history)-1].Content == "" {
		t.Fatal("cancelling finalization discarded the completed answer")
	}
	unlock()
	ctrl.Send("Say hello again.")
	select {
	case done := <-sink.done:
		if done.Cancelled || done.Err != nil || ctrl.TurnInFlight() {
			t.Fatalf("follow-up completion = %+v", done)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("follow-up could not run after cancelling finalization")
	}
	if n := len(p.requests()); n != 2 {
		t.Fatalf("provider requests including follow-up = %d, want 2", n)
	}
}
