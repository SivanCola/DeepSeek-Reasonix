package serve

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"reasonix/internal/contract/agentcomposition"
)

func TestCompositionRefusalNamesResourceAndKeepsDiagnostics(t *testing.T) {
	for _, tc := range []struct{ input, output, module string }{
		{"agent_composition.mcp_unavailable", "agent_composition.mcp_unavailable", "docs"},
		{"agent_composition.unavailable", "agent_composition.unavailable", "extension:tools"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			err := &agentcomposition.DiagnosticError{Diagnostics: []agentcomposition.Diagnostic{{Code: tc.input, Module: tc.module, Detail: "fixture cause"}}}
			w := httptest.NewRecorder()
			compositionError(w, err)
			var reason Reason
			if err := json.Unmarshal(w.Body.Bytes(), &reason); err != nil {
				t.Fatal(err)
			}
			if w.Code != 409 || reason.Code != tc.output || reason.Params["module"] != tc.module || reason.Params["detail"] != "fixture cause" || reason.Params["diagnostics"] == nil {
				t.Fatalf("resource identity lost: %s", w.Body.String())
			}
		})
	}
}
