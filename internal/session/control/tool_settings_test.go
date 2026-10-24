package control

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/config"
	"reasonix/internal/contract/tool"
)

type settingTool struct {
	name, desc, needed string
}

func (s settingTool) Name() string            { return s.name }
func (s settingTool) Description() string     { return s.desc }
func (s settingTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s settingTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (s settingTool) ReadOnly() bool { return true }

type neededTool struct{ settingTool }

func (n neededTool) HostRequiredBy() string { return n.needed }

func toolSettingNames(s ToolSettings) []string {
	var out []string
	for _, t := range s.Tools {
		out = append(out, t.Name)
	}
	return out
}

func TestToolSettingsListWhatTheRequestCarriesAndWhatTheUserRemoved(t *testing.T) {
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	reg := tool.NewRegistry()
	reg.Add(settingTool{name: "web_fetch", desc: "Fetch a page.\nSecond line."})
	reg.Add(neededTool{settingTool{name: "use_capability", desc: "Reach a deferred tool.", needed: tool.FeatureDeferredMCP}})
	c := New(Options{Registry: reg})

	if err := c.SaveToolDisabled("web_fetch", true); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !slices.Contains(config.LoadForEdit(config.UserConfigPath()).DisabledTools(), "web_fetch") {
		t.Fatal("the removal did not reach the user file")
	}
	got := c.ToolSettings()
	if !slices.Equal(toolSettingNames(got), []string{"use_capability", "web_fetch"}) {
		t.Fatalf("tools = %v, want the carried one and the removed one", toolSettingNames(got))
	}
	for _, s := range got.Tools {
		switch s.Name {
		case "web_fetch":
			if !s.Disabled {
				t.Fatal("web_fetch is not marked disabled")
			}
		case "use_capability":
			if s.Disabled || s.RequiredBy != tool.FeatureDeferredMCP {
				t.Fatalf("use_capability = %+v, want enabled with who needs it", s)
			}
		}
	}
	if err := c.SaveToolDisabled("web_fetch", false); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if slices.Contains(config.LoadForEdit(config.UserConfigPath()).DisabledTools(), "web_fetch") {
		t.Fatal("re-enabling did not reach the user file")
	}
}

func TestSaveToolDisabledRefusesWhatTheHostNeeds(t *testing.T) {
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	reg := tool.NewRegistry()
	reg.Add(neededTool{settingTool{name: "use_capability", needed: tool.FeatureDeferredMCP}})
	c := New(Options{Registry: reg})
	err := c.SaveToolDisabled("use_capability", true)
	var required *ToolRequiredError
	if !errors.As(err, &required) || required.Tool != "use_capability" || required.By != tool.FeatureDeferredMCP {
		t.Fatalf("err = %v, want a ToolRequiredError naming who needs it", err)
	}
	if len(config.LoadForEdit(config.UserConfigPath()).DisabledTools()) != 0 {
		t.Fatal("a refused removal reached the user file")
	}
}

func TestToolSettingsMarkARemovalOnlyTheProjectMade(t *testing.T) {
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	reg := tool.NewRegistry()
	reg.Add(settingTool{name: "web_fetch", desc: "Fetch a page."})
	reg.Add(settingTool{name: "recall", desc: "Recall."})
	reg.DisableTools([]string{"recall"})
	c := New(Options{Registry: reg})

	got := c.ToolSettings()
	if !slices.Equal(toolSettingNames(got), []string{"recall", "web_fetch"}) {
		t.Fatalf("tools = %v, want the carried one and the one the project removed", toolSettingNames(got))
	}
	for _, s := range got.Tools {
		switch s.Name {
		case "recall":
			if !s.Disabled || !s.ByProject {
				t.Fatalf("recall = %+v, want disabled and attributed to the project", s)
			}
		case "web_fetch":
			if s.Disabled || s.ByProject {
				t.Fatalf("web_fetch = %+v, want enabled", s)
			}
		}
	}

	if err := c.SaveToolDisabled("recall", true); err != nil {
		t.Fatalf("save: %v", err)
	}
	for _, s := range c.ToolSettings().Tools {
		if s.Name == "recall" && s.ByProject {
			t.Fatal("a removal the user file also makes is attributed to the project; the user's switch must own it")
		}
	}
}

func TestFirstDescriptionLineNeverCutsAMultibyteCharacter(t *testing.T) {
	line := firstDescriptionLine(strings.Repeat("工具", 150))
	if !utf8.ValidString(line) {
		t.Fatalf("the cut left invalid UTF-8: %q", line)
	}
	if n := utf8.RuneCountInString(line); n > 201 {
		t.Fatalf("line is %d characters, want at most 200 plus the ellipsis", n)
	}
	if !strings.HasSuffix(line, "…") {
		t.Fatalf("a shortened line should say so: %q", line)
	}
	if got := firstDescriptionLine("short line\nsecond line"); got != "short line" {
		t.Fatalf("first line = %q", got)
	}
}
