package control

import (
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestBranchAndSwitch(t *testing.T) {
	dir := t.TempDir()
	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	exec.Session().Add(provider.Message{Role: provider.RoleUser, Content: "root prompt"})
	c := New(Options{Executor: exec, SessionDir: dir, Label: "test"})
	c.SetSessionPath(agent.NewSessionPath(dir, "test"))
	if err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	rootPath := c.SessionPath()
	rootID := agent.BranchID(rootPath)

	if _, err := c.Branch("try something"); err != nil {
		t.Fatal(err)
	}
	childPath := c.SessionPath()
	if childPath == rootPath {
		t.Fatal("branch should switch to a new session path")
	}
	meta, ok, err := agent.LoadBranchMeta(childPath)
	if err != nil || !ok {
		t.Fatalf("load child meta ok=%v err=%v", ok, err)
	}
	if meta.ParentID != rootID || meta.Name != "try something" {
		t.Fatalf("child meta = %+v, want parent %q and name", meta, rootID)
	}

	if _, err := c.SwitchBranch(rootID); err != nil {
		t.Fatal(err)
	}
	if c.SessionPath() != rootPath {
		t.Fatalf("session path = %q, want %q", c.SessionPath(), rootPath)
	}

	tree := c.BranchTreeText()
	if !strings.Contains(tree, rootID) || !strings.Contains(tree, "try something") {
		t.Fatalf("tree missing expected branches:\n%s", tree)
	}
}

func TestFormatBranchTreeMarksCurrent(t *testing.T) {
	branches := []agent.BranchInfo{
		{BranchMeta: agent.BranchMeta{ID: "root"}, Preview: "root", Turns: 1},
		{BranchMeta: agent.BranchMeta{ID: "child", ParentID: "root", Name: "child branch"}, Turns: 2},
	}
	got := FormatBranchTree(branches, "child")
	if !strings.Contains(got, "* child") {
		t.Fatalf("tree should mark current branch:\n%s", got)
	}
}
