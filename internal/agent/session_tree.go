package agent

import (
	"encoding/json"
	"fmt"
	"io"

	"reasonix/internal/provider"
)

// SessionNode is one node in a session tree. The Messages field holds the
// conversation history leading to this node (inherited from ancestors plus
// any messages added directly to this node). Branching creates a new node
// sharing the parent's messages at the time of fork.
//
// Metadata carries arbitrary string annotations (goal state, branch label,
// creation timestamp) without entering the model's conversation — it never
// reaches the API, so it can change without affecting the cache prefix.
type SessionNode struct {
	ID       string
	ParentID string

	// Messages is the full message history at this node (system +
	// inherited + local). It is populated on construction and updated
	// only through Add() — never mutated in place.
	Messages []provider.Message

	// Metadata holds arbitrary annotations. Safe to mutate without
	// affecting cache (it is never serialized to the model).
	Metadata map[string]string

	rewriteVersion int
	turnCount        int
	cumulativeTokens int

	children []*SessionNode // owned by parent, not exported
	parent   *SessionNode
}

// RewriteVersion returns the rewrite counter (bumped on compaction at this node).
func (n *SessionNode) RewriteVersion() int { return n.rewriteVersion }

// IncrementRewrite bumps the rewrite counter.
func (n *SessionNode) IncrementRewrite() { n.rewriteVersion++ }

// TurnCount returns the number of turns completed at this node.
func (n *SessionNode) TurnCount() int { return n.turnCount }

// IncrementTurn advances the turn counter.
func (n *SessionNode) IncrementTurn() { n.turnCount++ }

// CumulativeTokens returns the total tokens used across all turns at this node.
func (n *SessionNode) CumulativeTokens() int { return n.cumulativeTokens }

// AddTokens adds n to the cumulative token count.
func (n *SessionNode) AddTokens(v int) { n.cumulativeTokens += v }

// Add appends a message to this node's history.
func (n *SessionNode) Add(m provider.Message) {
	n.Messages = append(n.Messages, m)
}

// Children returns this node's direct child branches.
func (n *SessionNode) Children() []*SessionNode { return n.children }

// Parent returns the parent node, nil for the root.
func (n *SessionNode) Parent() *SessionNode { return n.parent }

// SessionTree manages a tree of session nodes, supporting branch, merge,
// and navigation operations. It wraps the linear Session for backward
// compatibility while adding branching semantics.
type SessionTree struct {
	nodes   map[string]*SessionNode
	root    *SessionNode
	current *SessionNode
	nextID  int
}

// NewSessionTree creates a tree rooted at a node with an optional system
// prompt. The root id is "root".
func NewSessionTree(system string) *SessionTree {
	msgs := []provider.Message{}
	if system != "" {
		msgs = append(msgs, provider.Message{Role: provider.RoleSystem, Content: system})
	}
	root := &SessionNode{ID: "root", Messages: msgs, Metadata: map[string]string{}}
	st := &SessionTree{
		nodes:   map[string]*SessionNode{"root": root},
		root:    root,
		current: root,
		nextID:  1,
	}
	return st
}

// Current returns the active node.
func (st *SessionTree) Current() *SessionNode { return st.current }

// Root returns the root node.
func (st *SessionTree) Root() *SessionNode { return st.root }

// Node looks up a node by id. Returns nil if not found.
func (st *SessionTree) Node(id string) *SessionNode { return st.nodes[id] }

// Branch creates a new child node from the current node with an optional
// label stored in Metadata["label"]. The new node inherits a copy of the
// parent's messages at creation time; subsequent adds to either branch do
// not affect the other. Returns the new node and sets it as current.
func (st *SessionTree) Branch(label string) *SessionNode {
	id := fmt.Sprintf("b%d", st.nextID)
	st.nextID++

	msgs := make([]provider.Message, len(st.current.Messages))
	copy(msgs, st.current.Messages)

	child := &SessionNode{
		ID:       id,
		ParentID: st.current.ID,
		Messages: msgs,
		Metadata: map[string]string{"label": label},
		parent:   st.current,
	}
	st.current.children = append(st.current.children, child)
	st.nodes[id] = child
	st.current = child
	return child
}

// SwitchTo navigates to the node with the given id. Returns an error if
// the id is not found.
func (st *SessionTree) SwitchTo(id string) error {
	n := st.nodes[id]
	if n == nil {
		return fmt.Errorf("session tree: unknown node %q", id)
	}
	st.current = n
	return nil
}

// ToSession converts the current node to a linear Session for use with
// the existing Agent. The returned session shares the node's messages
// and counters.
func (st *SessionTree) ToSession() *Session {
	return &Session{
		Messages:         st.current.Messages,
		rewriteVersion:   st.current.rewriteVersion,
		turnCount:        st.current.turnCount,
		cumulativeTokens: st.current.cumulativeTokens,
	}
}

// SyncFrom updates the current node's counters from a Session.
func (st *SessionTree) SyncFrom(s *Session) {
	st.current.rewriteVersion = s.RewriteVersion()
	st.current.turnCount = s.TurnCount()
	st.current.cumulativeTokens = s.CumulativeTokens()
	st.current.Messages = s.Messages
}

// Nodes returns all node ids in breadth-first order.
func (st *SessionTree) Nodes() []string {
	seen := map[string]bool{}
	var out []string
	var walk func(n *SessionNode)
	walk = func(n *SessionNode) {
		if seen[n.ID] {
			return
		}
		seen[n.ID] = true
		out = append(out, n.ID)
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(st.root)
	return out
}

// PathTo returns the chain of node ids from root to the given node,
// inclusive. Returns nil if the node is not found.
func (st *SessionTree) PathTo(id string) []string {
	n := st.nodes[id]
	if n == nil {
		return nil
	}
	var ids []string
	for n != nil {
		ids = append([]string{n.ID}, ids...)
		n = n.parent
	}
	return ids
}

// --- Serialization ---

type wireNode struct {
	ID               string            `json:"id"`
	ParentID         string            `json:"parent_id,omitempty"`
	Messages         []provider.Message `json:"messages"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	RewriteVersion   int               `json:"rewrite_version"`
	TurnCount        int               `json:"turn_count"`
	CumulativeTokens int               `json:"cumulative_tokens"`
}

// Save writes the full tree as a JSONL stream (one wireNode per line,
// identical framing to Session persistence). The root is written first,
// then children in breadth-first order.
func (st *SessionTree) Save(w io.Writer) error {
	enc := json.NewEncoder(w)
	ids := st.Nodes()
	for _, id := range ids {
		n := st.nodes[id]
		wn := wireNode{
			ID:               n.ID,
			ParentID:         n.ParentID,
			Messages:         n.Messages,
			Metadata:         n.Metadata,
			RewriteVersion:   n.rewriteVersion,
			TurnCount:        n.turnCount,
			CumulativeTokens: n.cumulativeTokens,
		}
		if err := enc.Encode(wn); err != nil {
			return fmt.Errorf("session tree save: %w", err)
		}
	}
	return nil
}

// LoadTree reads a JSONL stream produced by Save and reconstructs the
// session tree. The last node in the stream is set as current.
func LoadTree(r io.Reader) (*SessionTree, error) {
	dec := json.NewDecoder(r)
	var st *SessionTree
	for {
		var wn wireNode
		if err := dec.Decode(&wn); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("session tree load: %w", err)
		}
		n := &SessionNode{
			ID:               wn.ID,
			ParentID:         wn.ParentID,
			Messages:         wn.Messages,
			Metadata:         wn.Metadata,
			rewriteVersion:   wn.RewriteVersion,
			turnCount:        wn.TurnCount,
			cumulativeTokens: wn.CumulativeTokens,
		}
		if st == nil {
			st = &SessionTree{
				nodes:  map[string]*SessionNode{wn.ID: n},
				root:   n,
				nextID: 1,
			}
			st.current = n
		} else {
			parent := st.nodes[wn.ParentID]
			if parent != nil {
				n.parent = parent
				parent.children = append(parent.children, n)
			}
			st.nodes[wn.ID] = n
			st.current = n
		}
	}
	if st == nil {
		return nil, fmt.Errorf("session tree load: empty stream")
	}
	return st, nil
}
