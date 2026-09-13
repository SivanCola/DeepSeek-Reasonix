package control

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	goaldomain "reasonix/internal/goal"
	"reasonix/internal/sessionv3"
	"reasonix/internal/tool"
)

func TestGoalDiagnosticExportReadsCompleteDurableV3Log(t *testing.T) {
	service, err := sessionv3.NewService("desktop", sessionv3.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), sessionv3.CreateOptions{SessionID: "goal-diagnostic"})
	if err != nil {
		t.Fatal(err)
	}
	toolPayload := json.RawMessage(`{"id":"call-1","name":"bash","output":"full diagnostic output; Authorization: Bearer secret-token-123456; api_key=sk-proj-1234567890abcdef"}`)
	if _, err := runtime.Session().AppendBatch(t.Context(), "tool-evidence", []sessionv3.Event{{Kind: "tool/result", Payload: toolPayload}}); err != nil {
		t.Fatal(err)
	}
	machine := goaldomain.NewMachine(nil, func() string { return "goal-1" })
	if _, err := machine.Create(goaldomain.CreateRequest{Objective: "diagnose the goal"}); err != nil {
		t.Fatal(err)
	}
	goalPayload, err := machine.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "goal:goal-1:1:create", []sessionv3.Event{{Kind: "goal/state", Payload: goalPayload}}); err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSessionV3: true})
	t.Cleanup(c.Close)
	payload, err := c.ExportGoalDiagnostics(t.Context(), GoalDiagnosticMetadata{ApplicationVersion: "1.2.3", BuildCommit: "abc", ProtocolVersion: 4, Capabilities: []string{"goal-lifecycle-v2"}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, want := range []string{`"schemaVersion": 1`, `"applicationVersion": "1.2.3"`, `"sessionCodec": "reasonix.session.linear/v3.1"`, `"full diagnostic output`, `"goal-lifecycle-v2"`, `"activationChanges"`, `"activation": "armed"`, `"inferred": true`, `"persistenceStatus": "ready"`, `"unavailable"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("diagnostic export missing %s:\n%s", want, text)
		}
	}
	for _, secret := range []string{"secret-token-123456", "sk-proj-1234567890abcdef"} {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostic export leaked %q:\n%s", secret, text)
		}
	}
}

func TestGoalDiagnosticExportSurvivesFlushFailureAndIncludesAcceptedPrefix(t *testing.T) {
	store, err := sessionv3.CreateWithOptions(t.TempDir()+"/goal-diagnostic-failure", "goal-diagnostic-failure", sessionv3.OpenOptions{
		Sync: func(*os.File) error { return errors.New("injected sync failure: api_key=sk-proj-secret123") },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := sessionv3.NewService("desktop", failingFlushPersistence{session: store})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), sessionv3.CreateOptions{SessionID: "goal-diagnostic-failure"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "accepted-only", []sessionv3.Event{{Kind: "tool/result", Payload: json.RawMessage(`{"id":"call-accepted","name":"bash","output":"accepted evidence"}`)}}); err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := New(Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSessionV3: true})
	t.Cleanup(c.Close)

	payload, err := c.ExportGoalDiagnostics(t.Context(), GoalDiagnosticMetadata{})
	if err != nil {
		t.Fatalf("ExportGoalDiagnostics: %v", err)
	}
	text := string(payload)
	for _, want := range []string{`"acceptedThrough": 1`, `"durableThrough": 0`, `"persistenceStatus": "uncertain"`, `"accepted evidence"`, `"durability checkpoint failed:`} {
		if !strings.Contains(text, want) {
			t.Fatalf("diagnostic export missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, "sk-proj-secret123") {
		t.Fatalf("diagnostic export leaked persistence error credential:\n%s", text)
	}
}
