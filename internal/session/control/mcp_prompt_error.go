package control

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"reasonix/internal/base/secrets"
)

// ErrMCPPromptFetch marks a prompt the user invoked whose text could not be
// obtained from its server.
var ErrMCPPromptFetch = errors.New("mcp prompt fetch failed")

const (
	mcpPromptStageGet     = "prompts/get"
	maxPromptFailuresOwed = 5
)

// MCPPromptError carries which prompt, which server and which stage failed;
// Err is the transport's own error, whose text the plugin layer already
// redacts.
type MCPPromptError struct {
	Prompt string
	Server string
	Stage  string
	Err    error
}

func (e *MCPPromptError) Error() string {
	return fmt.Sprintf("MCP prompt %q (server %q) failed at %s: %s", e.Prompt, e.Server, e.Stage, e.reason())
}

func (e *MCPPromptError) Unwrap() []error { return []error{ErrMCPPromptFetch, e.Err} }

func (e *MCPPromptError) reason() string {
	if e.Err == nil {
		return ""
	}
	return secrets.DiagnosticError(e.Err).Error()
}

var promptNoteEscaper = strings.NewReplacer("<", "&lt;", ">", "&gt;")

func (e *MCPPromptError) turnNote() string {
	return fmt.Sprintf("The user invoked MCP prompt %q (server %q); %s failed and no prompt text was received: %s",
		promptNoteEscaper.Replace(e.Prompt), promptNoteEscaper.Replace(e.Server), e.Stage, promptNoteEscaper.Replace(e.reason()))
}

// promptFailureDebt holds failures the model has not yet been told about. It
// keeps the notes until the model-visible history carries them: a turn refused
// before the provider delivered none of them, and asking the history is what
// lets the next turn owe them again.
type promptFailureDebt struct {
	mu    sync.Mutex
	notes []string
	// sees is the model-visible history (agent.SeesProjection); nil means no
	// model is attached and every note is still owed.
	sees func(string) bool
}

func (d *promptFailureDebt) owe(note string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.notes) >= maxPromptFailuresOwed {
		d.notes = d.notes[1:]
	}
	d.notes = append(d.notes, note)
}

// owed returns the notes the model cannot read yet. A note already present
// verbatim is not repeated: the block it rides is derived from the history, so
// this needs no settle at the hand-off.
func (d *promptFailureDebt) owed() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	live := make([]string, 0, len(d.notes))
	for _, note := range d.notes {
		if d.sees != nil && d.sees(note) {
			continue
		}
		live = append(live, note)
	}
	return strings.Join(live, "\n")
}
