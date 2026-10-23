package delegation

import (
	"strings"
	"testing"
)

func TestTaskDescriptionSaysASubAgentIsNotANewSession(t *testing.T) {
	desc := (&TaskTool{}).Description()
	for _, want := range []string{"not a new session", "max_subagent_depth"} {
		if !strings.Contains(desc, want) {
			t.Errorf("task description must contain %q so the model can tell a sub-agent from a new session: %s", want, desc)
		}
	}
}
