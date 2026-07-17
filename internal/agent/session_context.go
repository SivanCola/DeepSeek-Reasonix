package agent

import (
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// SessionContextVersion is the pinned session-context message format.
const SessionContextVersion = 2

// SessionContextSections is the deterministic input for rendering a
// session-context-v2 synthetic user message. Empty sections still render their
// heading so layout stays byte-stable for a given contract.
type SessionContextSections struct {
	Workspace    string // workspace root and related facts
	Environment  string // environment snapshot
	Instructions string // REASONIX.md / AGENTS.md / memory
	Skills       string // skills index body
}

// RenderSessionContext builds the pinned synthetic user message body.
// Rules: fixed section order, LF newlines, trailing newline, escape forged
// closing tags inside content.
func RenderSessionContext(contract RuntimeContract, sections SessionContextSections) string {
	c := NormalizeRuntimeContract(&contract)
	var b strings.Builder
	fmt.Fprintf(&b, "<session-context version=\"%d\"\n  tool-surface=%q\n  edit-protocol=%q>\n\n",
		SessionContextVersion, c.ToolSurface, c.EditProtocol)

	writeSection(&b, "Workspace", sections.Workspace)
	writeSection(&b, "Environment", sections.Environment)
	writeSection(&b, "Project and user instructions", sections.Instructions)
	writeSection(&b, "Skills", sections.Skills)

	b.WriteString("</session-context>\n")
	return b.String()
}

func writeSection(b *strings.Builder, title, body string) {
	b.WriteString("## ")
	b.WriteString(title)
	b.WriteByte('\n')
	body = escapeSessionContext(strings.ReplaceAll(body, "\r\n", "\n"))
	body = strings.TrimRight(body, "\n")
	if body != "" {
		b.WriteString(body)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// escapeSessionContext neutralizes forged closing tags in section content.
func escapeSessionContext(s string) string {
	return strings.ReplaceAll(s, "</session-context>", "</ session-context>")
}

// SessionContextMessage builds the synthetic user message with metadata.
func SessionContextMessage(contract RuntimeContract, sections SessionContextSections) provider.Message {
	return SyntheticUser(provider.SyntheticSessionContext, RenderSessionContext(contract, sections))
}

// IsSessionContextMessage reports whether m is a host-pinned session-context
// message. Prefer SyntheticReason; text prefix is only a legacy fallback.
func IsSessionContextMessage(m provider.Message) bool {
	if m.Role != provider.RoleUser {
		return false
	}
	if m.SyntheticReason == provider.SyntheticSessionContext {
		return true
	}
	return isStrictSessionContextBody(m.Content)
}

// isStrictSessionContextBody requires the opening tag form produced by
// RenderSessionContext (version + tool-surface + edit-protocol attributes).
// Bare user text starting with "<session-context" does not qualify.
func isStrictSessionContextBody(content string) bool {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, "<session-context") {
		return false
	}
	// Must look like our host renderer, not free-form user content.
	if !strings.Contains(s, `version="`) {
		return false
	}
	if attrValue(s, "tool-surface") == "" || attrValue(s, "edit-protocol") == "" {
		return false
	}
	if !strings.Contains(s, "</session-context>") {
		return false
	}
	return true
}

// FindSessionContextIndex returns the index of the pinned session-context
// message, or -1 if absent. Host layout places it immediately after system
// (index 1 when system is present).
func FindSessionContextIndex(msgs []provider.Message) int {
	// Strict position: only accept index 1 after a system message, or index 0
	// when there is no system (tests). Never scan arbitrary later user turns.
	if len(msgs) == 0 {
		return -1
	}
	if msgs[0].Role == provider.RoleSystem {
		if len(msgs) > 1 && IsSessionContextMessage(msgs[1]) {
			return 1
		}
		return -1
	}
	if IsSessionContextMessage(msgs[0]) {
		return 0
	}
	return -1
}

// InferRuntimeContractFromMessages attempts to recover a contract from a
// session-context message when the sidecar is missing/corrupt. Only the
// host-positioned message (immediately after system) with strict attributes is
// accepted; otherwise ok=false and callers must fall back to legacy.
func InferRuntimeContractFromMessages(msgs []provider.Message) (RuntimeContract, bool) {
	idx := FindSessionContextIndex(msgs)
	if idx < 0 {
		return RuntimeContract{}, false
	}
	// Must be immediately after system when a system message exists.
	if idx == 1 && msgs[0].Role != provider.RoleSystem {
		return RuntimeContract{}, false
	}
	if idx > 1 {
		return RuntimeContract{}, false
	}
	content := msgs[idx].Content
	if !isStrictSessionContextBody(content) && msgs[idx].SyntheticReason != provider.SyntheticSessionContext {
		return RuntimeContract{}, false
	}
	surface := attrValue(content, "tool-surface")
	edit := attrValue(content, "edit-protocol")
	if surface == "" || edit == "" {
		// Metadata-only synthetic message without render attributes cannot be inferred.
		return RuntimeContract{}, false
	}
	c := RuntimeContract{
		ToolSurface:  surface,
		EditProtocol: edit,
		PromptLayout: PromptLayoutSessionCtx,
	}
	if err := ValidateRuntimeContract(c); err != nil {
		return RuntimeContract{}, false
	}
	return c, true
}

func attrValue(s, key string) string {
	// Parse tool-surface="..." or tool-surface='...' from the opening tag region.
	needle := key + "="
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	rest := s[i+len(needle):]
	if rest == "" {
		return ""
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		return ""
	}
	rest = rest[1:]
	j := strings.IndexByte(rest, quote)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
