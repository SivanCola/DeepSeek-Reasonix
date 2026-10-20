package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"reasonix/internal/contract/agentcomposition"
)

func TestAnUnavailableCompositionNamesTheModuleAndWhy(t *testing.T) {
	err := &agentcomposition.DiagnosticError{Diagnostics: []agentcomposition.Diagnostic{
		{Code: "agent_composition.unavailable", Module: "tools:browser_act", Detail: "Required module is unavailable or was revoked."},
		{Code: "agent_composition.unavailable", Module: "skills:/review", Detail: "Required module is unavailable or was revoked."},
	}}
	w := httptest.NewRecorder()
	compositionError(w, err)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d", w.Code)
	}
	var got Reason
	if e := json.NewDecoder(w.Body).Decode(&got); e != nil {
		t.Fatal(e)
	}
	if got.Code != "agent_composition.unavailable" {
		t.Fatalf("code %q", got.Code)
	}
	if got.Params["module"] != "tools:browser_act, skills:/review" {
		t.Fatalf("module param = %v", got.Params["module"])
	}
	if got.Params["detail"] != "Required module is unavailable or was revoked." {
		t.Fatalf("detail param = %v", got.Params["detail"])
	}
}

func TestAnUnavailableCompositionWithoutADiagnosticStillSaysWhy(t *testing.T) {
	w := httptest.NewRecorder()
	compositionError(w, fmt.Errorf("%w: tool contract changed", agentcomposition.ErrUnavailable))
	var got Reason
	if e := json.NewDecoder(w.Body).Decode(&got); e != nil {
		t.Fatal(e)
	}
	if got.Params["module"] != "composition" || got.Params["detail"] != "tool contract changed" {
		t.Fatalf("params = %v", got.Params)
	}
}
