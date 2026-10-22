package boot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/runtime/agent"
)

const scriptUnderTest = `
call("write_file", path="made.txt", content="from the script")
print(call("read_file", path="made.txt"))
r = try_call("bash", command="rm -rf made.txt")
print("rm ok:", r.ok)
`

// run_script's calls pass the same stages the model's own do: the write
// lands and is owed verification, the read sees it, the deny rule refuses rm, each call shows as its own
// card, and only the script's printed result enters the conversation.
func TestEffectRunScriptCallsPassTheOrdinaryChecks(t *testing.T) {
	t.Parallel()
	home := statedBootHome(t)
	writeFile(t, home, "config.toml", holdChangedTurns)
	dir := testenv.TempDir(t)
	rec := &browserScriptProvider{rounds: []func(string) *provider.ToolCall{
		func(string) *provider.ToolCall {
			return browserCall("s1", "run_script", map[string]any{"script": scriptUnderTest})
		},
	}}
	kind := "boot-script-" + strings.ToLower(t.Name())
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return rec, nil })
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"
tool_approval = "yolo"

[agent]
system_prompt = "BASE"
code_mode = true

[permissions]
deny = ["Bash(rm:*)"]

[codegraph]
enabled = false

[[providers]]
name = "test-model"
kind = "`+kind+`"
model = "x"
`)
	approveWorkspaceAt(t, home, dir)
	sink := &noticeRecorder{}
	ctrl, err := Build(context.Background(), Options{Home: home, WorkspaceRoot: dir, Sink: sink})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// The nested write is one the turn owes verification for, exactly as a
	// direct write_file would be; the readiness gate saying so is the proof.
	var unready *agent.FinalReadinessError
	if err := ctrl.Run(context.Background(), "make a file with a script"); !errors.As(err, &unready) {
		t.Fatalf("Run = %v, want the readiness gate to ask for verification of the nested write", err)
	}
	ctrl.Close()
	reqs := agentRequests(rec.requests())
	if !toolNames(reqs[0])["run_script"] {
		t.Fatalf("run_script missing from the schema: %v", toolSchemaNames(reqs[0].Tools))
	}
	if body, err := os.ReadFile(filepath.Join(dir, "made.txt")); err != nil || string(body) != "from the script" {
		t.Fatalf("made.txt = %q, %v", body, err)
	}
	results := effectToolResults(reqs[len(reqs)-1])
	if len(results) != 1 {
		t.Fatalf("tool results = %q, want only the script's", results)
	}
	if !strings.Contains(results[0], "from the script") || !strings.Contains(results[0], "rm ok: False") {
		t.Fatalf("script result = %q, want the read and a refused rm", results[0])
	}
	cards := map[string]bool{}
	sink.mu.Lock()
	for _, e := range sink.events {
		if e.Kind == event.ToolResult {
			cards[e.Tool.ID+" "+e.Tool.Name] = true
		}
	}
	sink.mu.Unlock()
	for _, want := range []string{"s1.1 write_file", "s1.2 read_file", "s1.3 bash", "s1 run_script"} {
		if !cards[want] {
			t.Fatalf("no tool card %q among %v", want, cards)
		}
	}
}

const rewriteScript = `
for p in ["plain.go", "bom.go", "crlf.txt", "big.txt"]:
    src = call("read_file", path=p)
    call("write_file", path=p, content=src.replace("OldName", "NewName"))
print("rewrote")
`

// A script that reads a file, edits the text and writes it back changes only
// what it edited: no line numbers, no lost BOM or CRLF, and no window cut from
// a file larger than one model-facing result.
func TestEffectRunScriptReadEditWriteKeepsTheBytes(t *testing.T) {
	t.Parallel()
	home := statedBootHome(t)
	writeFile(t, home, "config.toml", holdChangedTurns)
	dir := testenv.TempDir(t)
	big := strings.Repeat("// filler line that pads the file past one tool result\n", 1200) + "func OldName() {}\n"
	files := map[string]string{
		"plain.go": "package p\n\nfunc OldName() {}\n",
		"bom.go":   "\ufeffpackage p\n\nfunc OldName() {}\n",
		"crlf.txt": "OldName\r\nsecond\r\nno final newline",
		"big.txt":  big,
	}
	for name, body := range files {
		writeFile(t, dir, name, body)
	}
	rec := &browserScriptProvider{rounds: []func(string) *provider.ToolCall{
		func(string) *provider.ToolCall {
			return browserCall("s1", "run_script", map[string]any{"script": rewriteScript})
		},
	}}
	kind := "boot-script-" + strings.ToLower(t.Name())
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return rec, nil })
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"
tool_approval = "yolo"

[agent]
system_prompt = "BASE"
code_mode = true

[codegraph]
enabled = false

[[providers]]
name = "test-model"
kind = "`+kind+`"
model = "x"
`)
	approveWorkspaceAt(t, home, dir)
	ctrl, err := Build(context.Background(), Options{Home: home, WorkspaceRoot: dir, Sink: &noticeRecorder{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_ = ctrl.Run(context.Background(), "rename OldName with a script")
	ctrl.Close()
	reqs := agentRequests(rec.requests())
	if results := effectToolResults(reqs[len(reqs)-1]); len(results) != 1 || !strings.Contains(results[0], "rewrote") {
		t.Fatalf("script result = %q", results)
	}
	for name, body := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.ReplaceAll(body, "OldName", "NewName"); string(got) != want {
			t.Errorf("%s:\n got %q\nwant %q", name, clip(string(got)), clip(want))
		}
	}
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:60] + "…" + s[len(s)-60:]
	}
	return s
}
