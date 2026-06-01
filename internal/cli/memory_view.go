package cli

import (
	"fmt"
	"strings"

	"reasonix/internal/memory"
)

func renderMemory(width int, set *memory.Set) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", viewHeader("memory"))
	if len(set.Docs) > 0 {
		b.WriteString(viewSubhead("docs") + "\n")
		for _, d := range set.Docs {
			scope := "(" + string(d.Scope) + ")"
			fmt.Fprintf(&b, "  %s  %s\n", viewMeta(scope), viewCompactPath(d.Path, viewBudget(width, 2+visibleWidth(scope)+2)))
		}
	}
	if strings.TrimSpace(set.Index) != "" {
		if len(set.Docs) > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(viewSubhead("saved memories") + "\n")
		fmt.Fprintf(&b, "  %s\n", viewCompactPath(set.Store.Dir, viewBudget(width, 2)))
	}
	b.WriteString("\n")
	b.WriteString(viewHint(viewCompactText("edit those files or use #<note>; changes apply next session", viewBudget(width, 2))))
	return strings.TrimRight(b.String(), "\n")
}
