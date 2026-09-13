package control

import (
	"encoding/json"
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
	toolPayload := json.RawMessage(`{"id":"call-1","name":"bash","output":"full diagnostic output"}`)
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
	for _, want := range []string{`"schemaVersion": 1`, `"applicationVersion": "1.2.3"`, `"sessionCodec": "reasonix.session.linear/v3.1"`, `"full diagnostic output"`, `"goal-lifecycle-v2"`, `"activationChanges"`, `"activation": "armed"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("diagnostic export missing %s:\n%s", want, text)
		}
	}
}
