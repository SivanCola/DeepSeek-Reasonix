package boot

import (
	"errors"

	"reasonix/internal/base/secrets"
	"reasonix/internal/contract/agentcomposition"
)

func compositionResourceError(kind, id string, err error) error {
	var diagnosed *agentcomposition.DiagnosticError
	if errors.As(err, &diagnosed) {
		return err
	}
	cause := secrets.DiagnosticError(err)
	code, module := "agent_composition.unavailable", kind+":"+id
	if kind == "mcp" {
		code, module = "agent_composition.mcp_unavailable", id
	}
	return errors.Join(&agentcomposition.DiagnosticError{Diagnostics: []agentcomposition.Diagnostic{{
		Code: code, Module: module, Detail: cause.Error(),
	}}}, cause)
}
