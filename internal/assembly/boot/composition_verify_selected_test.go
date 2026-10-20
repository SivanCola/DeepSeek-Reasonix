package boot

import (
	"errors"
	"testing"

	"reasonix/internal/contract/agentcomposition"
)

func TestAMissingSelectedModuleIsNamedByKindAndID(t *testing.T) {
	sel := agentcomposition.Selection{Mode: agentcomposition.Select, Members: []agentcomposition.Module{{ID: "browser_act"}}}
	err := verifySelected("tools", sel, []string{"read"}, func(v string) string { return v })
	var diagnosed *agentcomposition.DiagnosticError
	if !errors.As(err, &diagnosed) || len(diagnosed.Diagnostics) != 1 || diagnosed.Diagnostics[0].Module != "tools:browser_act" {
		t.Fatalf("err = %#v, want a diagnostic for tools:browser_act", err)
	}
}
