package capability

import (
	"strings"
	"testing"
)

func TestSearchExactMatchRanksFirst(t *testing.T) {
	entries := []Entry{
		{ID: "tool:grep_other", Kind: KindTool, Name: "grep_other", Description: "grep helper", Status: StatusReady},
		{ID: "tool:grep", Kind: KindTool, Name: "grep", Description: "search files", Status: StatusReady},
		{ID: "tool:glob", Kind: KindTool, Name: "glob", Description: "glob paths with grep-like patterns", Status: StatusReady},
	}
	hits := Search(entries, SearchOptions{Query: "grep", Limit: 5})
	if len(hits) == 0 {
		t.Fatal("expected hits")
	}
	if hits[0].Entry.ID != "tool:grep" || hits[0].Tier != searchTierExact {
		t.Fatalf("top hit = %+v, want exact tool:grep", hits[0])
	}
}

func TestSearchPrefixBeforeBM25(t *testing.T) {
	entries := []Entry{
		// BM25-friendly description with the query term, but no name/id prefix.
		{ID: "tool:search_code", Kind: KindTool, Name: "search_code", Description: "grep code base thoroughly", Status: StatusReady},
		// Prefix on name.
		{ID: "tool:gre", Kind: KindTool, Name: "gre_helper", Description: "unrelated", Status: StatusReady},
	}
	hits := Search(entries, SearchOptions{Query: "gre", Limit: 5})
	if len(hits) < 1 {
		t.Fatal("expected hits")
	}
	if hits[0].Entry.ID != "tool:gre" || hits[0].Tier != searchTierPrefix {
		t.Fatalf("top hit = %+v, want prefix tool:gre", hits[0])
	}
}

func TestSearchBM25ChineseAndEnglish(t *testing.T) {
	entries := []Entry{
		{ID: "skill:review", Kind: KindSkill, Name: "review", Description: "审查代码查找问题 code review", Status: StatusReady, Triggers: []string{"review", "审查"}},
		{ID: "skill:deploy", Kind: KindSkill, Name: "deploy", Description: "deploy services", Status: StatusReady},
		{ID: "tool:bash", Kind: KindTool, Name: "bash", Description: "run shell", Status: StatusReady},
	}
	en := Search(entries, SearchOptions{Query: "code review", Limit: 5, Kind: SearchKindSkill})
	if len(en) == 0 || en[0].Entry.ID != "skill:review" {
		t.Fatalf("EN hits = %+v, want skill:review first", en)
	}
	cn := Search(entries, SearchOptions{Query: "审查", Limit: 5, Kind: SearchKindSkill})
	if len(cn) == 0 || cn[0].Entry.ID != "skill:review" {
		t.Fatalf("CN hits = %+v, want skill:review first", cn)
	}
}

func TestSearchTieBreakByCapabilityID(t *testing.T) {
	// Two exact name matches for different IDs with identical fields → ID order.
	entries := []Entry{
		{ID: "tool:z_tool", Kind: KindTool, Name: "same", Description: "x", Status: StatusReady},
		{ID: "tool:a_tool", Kind: KindTool, Name: "same", Description: "x", Status: StatusReady},
	}
	hits := Search(entries, SearchOptions{Query: "same", Limit: 5})
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if hits[0].Entry.ID != "tool:a_tool" || hits[1].Entry.ID != "tool:z_tool" {
		t.Fatalf("tie-break order = %q, %q", hits[0].Entry.ID, hits[1].Entry.ID)
	}
}

func TestSearchSkipsDisabled(t *testing.T) {
	entries := []Entry{
		{ID: "mcp-server:off", Kind: KindMCPServer, Name: "off", Description: "github mcp", Status: StatusDisabled},
		{ID: "mcp-server:on", Kind: KindMCPServer, Name: "on", Description: "github mcp", Status: StatusReady},
	}
	hits := Search(entries, SearchOptions{Query: "github", Limit: 5, Kind: SearchKindMCP})
	for _, h := range hits {
		if h.Entry.Status == StatusDisabled {
			t.Fatalf("disabled entry returned: %+v", h)
		}
	}
	if len(hits) != 1 || hits[0].Entry.ID != "mcp-server:on" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearchKindFilter(t *testing.T) {
	entries := []Entry{
		{ID: "tool:grep", Kind: KindTool, Name: "grep", Description: "grep", Status: StatusReady},
		{ID: "skill:grep", Kind: KindSkill, Name: "grep", Description: "grep skill", Status: StatusReady},
		{ID: "mcp-tool:gh/search", Kind: KindMCPTool, Name: "gh/search", Description: "grep issues", Status: StatusReady},
	}
	hits := Search(entries, SearchOptions{Query: "grep", Limit: 10, Kind: SearchKindTool})
	if len(hits) != 1 || hits[0].Entry.ID != "tool:grep" {
		t.Fatalf("tool filter = %+v", hits)
	}
	mcp := Search(entries, SearchOptions{Query: "grep", Limit: 10, Kind: SearchKindMCP})
	if len(mcp) != 1 || !strings.HasPrefix(mcp[0].Entry.ID, "mcp-") {
		t.Fatalf("mcp filter = %+v", mcp)
	}
}

func TestSearchLimitClamp(t *testing.T) {
	entries := make([]Entry, 0, 12)
	for i := 0; i < 12; i++ {
		entries = append(entries, Entry{
			ID: "tool:item" + string(rune('a'+i)), Kind: KindTool, Name: "item", Description: "item", Status: StatusReady,
		})
	}
	hits := Search(entries, SearchOptions{Query: "item", Limit: 100})
	if len(hits) > 10 {
		t.Fatalf("limit not clamped: %d", len(hits))
	}
}

func TestIndexTextWeightsNameAndDescription(t *testing.T) {
	text := indexText(Entry{
		ID: "tool:x", Name: "alpha", Description: "beta", Source: "src",
		Triggers: []string{"trig"}, Requires: []string{"req"},
	})
	if strings.Count(text, "alpha") < 3 {
		t.Fatalf("name weight missing: %q", text)
	}
	if strings.Count(text, "beta") < 2 {
		t.Fatalf("description weight missing: %q", text)
	}
	for _, want := range []string{"tool:x", "src", "trig", "req"} {
		if !strings.Contains(text, want) {
			t.Fatalf("index missing %q in %q", want, text)
		}
	}
}
