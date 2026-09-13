package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessionv3"
	"reasonix/internal/tool"
)

func TestGoalDiagnosticsHTTPExportsAuthoritativeSession(t *testing.T) {
	service, err := sessionv3.NewService("serve", sessionv3.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), sessionv3.CreateOptions{SessionID: "diagnostic-http"})
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSessionV3: true})
	t.Cleanup(ctrl.Close)
	server := &Server{ctrl: ctrl}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/goal-diagnostics", nil)
	server.goalDiagnostics(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	for _, want := range []string{"reasonix.session.linear/v3", "goal-lifecycle-v2", "activationChanges"} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("diagnostic response missing %q: %s", want, recorder.Body.String())
		}
	}
}
