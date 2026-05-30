package agent

import (
	"bytes"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func TestSessionTreeBranch(t *testing.T) {
	st := NewSessionTree("You are a helpful assistant.")

	// Add a message to the root.
	root := st.Current()
	root.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	root.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi there"})

	// Branch from root.
	child1 := st.Branch("fix-bug")
	child1.Add(provider.Message{Role: provider.RoleUser, Content: "fix the bug"})

	// Root is unchanged.
	if len(root.Messages) != 3 { // system + user + assistant
		t.Fatalf("root should have 3 messages, got %d", len(root.Messages))
	}

	// Child has 4 messages: inherited 3 + new user.
	if len(child1.Messages) != 4 {
		t.Fatalf("child1 should have 4 messages, got %d", len(child1.Messages))
	}

	// Switch back to root and create another branch.
	if err := st.SwitchTo("root"); err != nil {
		t.Fatal(err)
	}
	child2 := st.Branch("add-feature")
	child2.Add(provider.Message{Role: provider.RoleUser, Content: "add a feature"})

	// child2 has 4 messages, unrelated to child1.
	if len(child2.Messages) != 4 {
		t.Fatalf("child2 should have 4 messages, got %d", len(child2.Messages))
	}
	if child2.Messages[3].Content != "add a feature" {
		t.Errorf("child2's extra message should be 'add a feature', got %q", child2.Messages[3].Content)
	}
}

func TestSessionTreeNavigation(t *testing.T) {
	st := NewSessionTree("")
	st.Current().Add(provider.Message{Role: provider.RoleUser, Content: "a"})

	st.Branch("b1")
	st.Current().Add(provider.Message{Role: provider.RoleAssistant, Content: "b1 reply"})

	st.SwitchTo("root")
	st.Branch("b2")

	nodes := st.Nodes()
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	path := st.PathTo(st.Current().ID)
	if len(path) != 2 || path[0] != "root" {
		t.Errorf("path from root to current: %v", path)
	}
}

func TestSessionTreeRoundTrip(t *testing.T) {
	st := NewSessionTree("sys")
	root := st.Current()
	root.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
	root.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	root.AddTokens(100)
	root.IncrementTurn()

	child := st.Branch("explore")
	child.Add(provider.Message{Role: provider.RoleUser, Content: "more"})
	child.AddTokens(50)

	var buf bytes.Buffer
	if err := st.Save(&buf); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadTree(&buf)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(loaded.Nodes()) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(loaded.Nodes()))
	}

	loadedRoot := loaded.Node("root")
	if loadedRoot == nil || len(loadedRoot.Messages) != 3 {
		t.Errorf("root: %d messages", len(loadedRoot.Messages))
	}
	if loadedRoot.CumulativeTokens() != 100 {
		t.Errorf("root cumulative: %d", loadedRoot.CumulativeTokens())
	}

	// After load, last node read is current.
	if loaded.Current().ID != child.ID {
		t.Errorf("current after load: %s", loaded.Current().ID)
	}
}

func TestSessionTreeToSession(t *testing.T) {
	st := NewSessionTree("sys")
	st.Current().Add(provider.Message{Role: provider.RoleUser, Content: "x"})
	st.Current().IncrementTurn()
	st.Current().AddTokens(42)

	sess := st.ToSession()
	if len(sess.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(sess.Messages))
	}
	if sess.TurnCount() != 1 {
		t.Errorf("turnCount: %d", sess.TurnCount())
	}
	if sess.CumulativeTokens() != 42 {
		t.Errorf("cumulativeTokens: %d", sess.CumulativeTokens())
	}
}

func TestSessionTreeBranchIsolation(t *testing.T) {
	st := NewSessionTree("")
	st.Current().Add(provider.Message{Role: provider.RoleUser, Content: "common"})

	b1 := st.Branch("b1")
	b1.Add(provider.Message{Role: provider.RoleAssistant, Content: "from b1"})

	st.SwitchTo("root")
	b2 := st.Branch("b2")
	b2.Add(provider.Message{Role: provider.RoleAssistant, Content: "from b2"})

	// b1 should not see b2's message.
	if strings.Contains(strings.Join(messagesText(b1.Messages), "|"), "from b2") {
		t.Error("b1 should not contain b2's message")
	}
	// b2 should not see b1's message.
	if strings.Contains(strings.Join(messagesText(b2.Messages), "|"), "from b1") {
		t.Error("b2 should not contain b1's message")
	}
}

func messagesText(msgs []provider.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}
