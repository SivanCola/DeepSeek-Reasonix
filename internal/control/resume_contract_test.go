package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestResumeAppliesSavedRuntimeContract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")

	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	if err := sess.Save(path); err != nil {
		t.Fatal(err)
	}
	hl := agent.DefaultRuntimeContract()
	hl.EditProtocol = agent.EditProtocolHashline
	if err := agent.SaveBranchMeta(path, agent.BranchMeta{
		ID:              agent.BranchID(path),
		RuntimeContract: hl.Clone(),
	}); err != nil {
		t.Fatal(err)
	}

	legacyReg := tool.NewRegistry()
	legacyReg.Add(&resumeSurfaceTool{name: "read_file"})
	classicReg := tool.NewRegistry()
	classicReg.Add(&resumeSurfaceTool{name: "read_file"})
	classicReg.Add(&resumeSurfaceTool{name: "use_capability"})
	hashReg := tool.NewRegistry()
	hashReg.Add(&resumeSurfaceTool{name: "hashline_read"})
	hashReg.Add(&resumeSurfaceTool{name: "use_capability"})
	execReg := tool.NewRegistry()
	execReg.Add(&resumeSurfaceTool{name: "grep"})

	bundle := &agent.RegistryBundle{
		Legacy:             legacyReg,
		CapabilityClassic:  classicReg,
		CapabilityHashline: hashReg,
		Execution:          execReg,
	}

	// Boots on classic capability surface.
	exec := agent.New(&resumeFakeProv{}, classicReg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	_ = exec.SetRuntimeContract(agent.DefaultRuntimeContract())
	exec.SetRegistryBundle(bundle)

	c := New(Options{Runner: exec, Executor: exec, Sink: event.Discard})
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Resume(loaded, path)

	if got := exec.RuntimeContract().EditProtocol; got != agent.EditProtocolHashline {
		t.Fatalf("after resume edit_protocol = %s, want hashline-v1", got)
	}
	// Bundle surface for the applied contract must be hashline.
	reg := bundle.ProviderRegistry(exec.RuntimeContract())
	if _, ok := reg.Get("hashline_read"); !ok {
		t.Fatalf("provider surface for resumed contract tools=%v, want hashline_read", reg.Names())
	}
	if _, ok := reg.Get("read_file"); ok {
		t.Fatal("classic read_file must not be on hashline provider surface")
	}
}

func TestResumeWithoutContractStaysLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.jsonl")
	// Transcript looks like session-context-v2 — must NOT be used when sidecar exists.
	c2 := agent.DefaultRuntimeContract()
	sess := agent.NewSession("sys")
	sess.Add(agent.SessionContextMessage(c2, agent.SessionContextSections{Workspace: "w"}))
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	if err := sess.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMeta(path, agent.BranchMeta{ID: agent.BranchID(path)}); err != nil {
		t.Fatal(err)
	}

	legacyReg := tool.NewRegistry()
	legacyReg.Add(&resumeSurfaceTool{name: "read_file"})
	classicReg := tool.NewRegistry()
	classicReg.Add(&resumeSurfaceTool{name: "use_capability"})
	bundle := &agent.RegistryBundle{
		Legacy:            legacyReg,
		CapabilityClassic: classicReg,
		Execution:         legacyReg,
	}
	// Process defaults to capability-v2; old session must not upgrade via inference.
	exec := agent.New(&resumeFakeProv{}, classicReg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	_ = exec.SetRuntimeContract(agent.DefaultRuntimeContract())
	exec.SetRegistryBundle(bundle)

	c := New(Options{Runner: exec, Executor: exec, Sink: event.Discard})
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Resume(loaded, path)
	if got := exec.RuntimeContract(); !got.Equal(agent.LegacyDefaultRuntimeContract()) {
		t.Fatalf("old session upgraded to %+v, want legacy (no transcript inference when sidecar exists)", got)
	}
}

func TestResumeCorruptSidecarForcesLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.jsonl")
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hi"})
	if err := sess.Save(path); err != nil {
		t.Fatal(err)
	}
	// Write unreadable/corrupt meta beside the session.
	metaPath := agent.BranchMetaPath(path)
	if err := os.WriteFile(metaPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}

	legacyReg := tool.NewRegistry()
	legacyReg.Add(&resumeSurfaceTool{name: "read_file"})
	classicReg := tool.NewRegistry()
	classicReg.Add(&resumeSurfaceTool{name: "use_capability"})
	hashReg := tool.NewRegistry()
	hashReg.Add(&resumeSurfaceTool{name: "hashline_read"})
	bundle := &agent.RegistryBundle{
		Legacy:             legacyReg,
		CapabilityClassic:  classicReg,
		CapabilityHashline: hashReg,
		Execution:          classicReg,
	}
	exec := agent.New(&resumeFakeProv{}, classicReg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	_ = exec.SetRuntimeContract(agent.DefaultRuntimeContract())
	exec.SetRegistryBundle(bundle)

	ctrl := New(Options{Runner: exec, Executor: exec, Sink: event.Discard})
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.Resume(loaded, path)
	if got := exec.RuntimeContract(); !got.Equal(agent.LegacyDefaultRuntimeContract()) {
		t.Fatalf("corrupt sidecar applied %+v, want legacy", got)
	}
	// Surface must switch off the polluted v2 provider registry.
	reg := bundle.ProviderRegistry(exec.RuntimeContract())
	if _, ok := reg.Get("use_capability"); ok {
		t.Fatal("legacy surface after corrupt sidecar must not expose use_capability")
	}
	if _, ok := reg.Get("read_file"); !ok {
		t.Fatal("legacy surface must keep read_file")
	}
}

type resumeSurfaceTool struct{ name string }

func (f *resumeSurfaceTool) Name() string            { return f.name }
func (f *resumeSurfaceTool) Description() string     { return f.name }
func (f *resumeSurfaceTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (f *resumeSurfaceTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}
func (f *resumeSurfaceTool) ReadOnly() bool { return true }

type resumeFakeProv struct{}

func (resumeFakeProv) Name() string { return "fake" }
func (resumeFakeProv) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk)
	close(ch)
	return ch, nil
}
