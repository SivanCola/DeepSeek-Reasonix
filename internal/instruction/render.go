package instruction

import (
	"fmt"
	"strings"
)

// Block renders resolved standing instructions in broad-to-specific order.
// Diagnostics stay host-side; malformed imports are already annotated at the
// exact source line and must not become additional model instructions.
func Block(documents []Document) string {
	if len(documents) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Instructions\n\n")
	b.WriteString("Standing guidance resolved for this workspace and target path. Later entries are more specific and take precedence when rules conflict; the current user request still has highest priority.\n")
	for _, doc := range documents {
		fmt.Fprintf(&b, "\n## %s (%s", doc.Path, doc.Scope)
		if doc.Scope != ScopeUser && strings.TrimSpace(doc.Directory) != "" {
			fmt.Fprintf(&b, ", applies to %s", doc.Directory)
		}
		b.WriteString(")\n\n")
		b.WriteString(strings.TrimSpace(doc.Body))
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func Compose(base string, documents []Document) string {
	block := Block(documents)
	if block == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return block
	}
	return strings.TrimRight(base, "\n") + "\n\n" + block
}
