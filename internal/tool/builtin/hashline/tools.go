package hashline

import (
	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// Config binds hashline tools to a workspace the same way classic built-ins are.
type Config struct {
	WorkDir     string
	WriteRoots  []string
	ForbidRoots []string
	Paths       *builtin.PathResolver
	Guard       builtin.SessionDataGuard
	Managed     builtin.ManagedConfigPaths
	Overlay     builtin.FileOverlay
	Search      builtin.SearchSpec
	Sandbox     sandbox.Spec
}

// Tools returns the three hashline tools bound to cfg.
// They are NOT registered into tool.Builtins(); boot must Add them only for
// hashline-protocol sessions.
func (c Config) Tools() []tool.Tool {
	writeRoots := c.WriteRoots
	if len(writeRoots) == 0 && c.WorkDir != "" {
		writeRoots = []string{c.WorkDir}
	}
	return []tool.Tool{
		NewRead(c.WorkDir, c.Paths, c.ForbidRoots, c.Overlay),
		NewEdit(c.WorkDir, writeRoots, c.Guard, c.Managed, c.Overlay),
		NewGrep(c.WorkDir, c.Paths, c.Search, c.Sandbox, c.ForbidRoots),
	}
}

// ToolNames are the provider-visible hashline tool ids.
var ToolNames = []string{"hashline_read", "hashline_edit", "hashline_grep"}
