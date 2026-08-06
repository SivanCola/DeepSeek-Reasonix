package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SearchToolName is the model-visible name of the deferred-tier search tool.
const SearchToolName = "tool_search"

const defaultSearchResults = 5

// searchTool matches a query against the deferred roster and activates what it
// finds, which is how a withheld tool becomes callable.
type searchTool struct{ reg *Registry }

// NewSearchTool returns the core-tier tool that searches the deferred roster.
//
// It is deliberately not a process-global built-in. It binds to one registry,
// and a host that defers nothing has no use for it — so hosts opt in by adding
// it, leaving the CLI and the full desktop exactly as they were.
func NewSearchTool(reg *Registry) Tool { return &searchTool{reg: reg} }

func (s *searchTool) Name() string { return SearchToolName }

// Description is static on purpose. It sits in the provider-visible prefix, so
// naming the currently-deferred tools here would rewrite the prefix every time
// an MCP server finished connecting. The roster is dynamic state and belongs in
// a turn-scoped message the host injects, not in this schema.
func (s *searchTool) Description() string {
	return "Search for tools that are available but not yet loaded, and load the ones you need so you can call them.\n\n" +
		"Some tools — typically MCP and skill capabilities — are held back to keep the tool list small. They are listed by name in the conversation but their parameters are not loaded, so they cannot be called until you load them here.\n\n" +
		"Query forms:\n" +
		"- \"select:read_sheet,write_sheet\" — load these exact tools by name\n" +
		"- \"spreadsheet excel\" — keyword search, best matches first\n" +
		"- \"+figma design\" — require \"figma\" in the name, rank by the rest\n\n" +
		"Loaded tools stay callable for the rest of the session, so search once per capability rather than before every call."
}

func (s *searchTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Keywords to match against tool names and descriptions, or \"select:<name>,<name>\" to load exact tools by name."},"max_results":{"type":"integer","description":"Maximum number of tools to load (default 5). Ignored for select: queries, which load every name given."}},"required":["query"]}`)
}

func (s *searchTool) ReadOnly() bool { return true }

func (s *searchTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	query := strings.TrimSpace(p.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if s.reg == nil {
		return "", fmt.Errorf("tool search is not available in this context")
	}

	roster := s.reg.DeferredRoster()
	if len(roster) == 0 {
		return "No tools are deferred in this session; every available tool is already loaded.", nil
	}

	matches := matchDeferred(roster, query, p.MaxResults)
	if len(matches) == 0 {
		return fmt.Sprintf("No deferred tool matched %q. %d tools are deferred — try broader keywords, or \"select:<name>\" if you know the exact name.", query, len(roster)), nil
	}

	// An unavailable tool is reported but never activated: its capability
	// cannot run, so spending permanent prefix bytes on its schema buys
	// nothing.
	var runnable, blocked []DeferredEntry
	for _, m := range matches {
		if m.Unavailable != "" {
			blocked = append(blocked, m)
			continue
		}
		runnable = append(runnable, m)
	}

	names := make([]string, 0, len(runnable))
	for _, m := range runnable {
		names = append(names, m.Name)
	}
	activated := s.reg.Activate(names...)
	activatedSet := make(map[string]bool, len(activated))
	for _, name := range activated {
		activatedSet[name] = true
	}

	return renderSearchResult(runnable, blocked, activatedSet), nil
}

// renderSearchResult reports names and descriptions but not schemas. The
// activated tools are appended to the registry's exported list, so their full
// parameters reach the model through the tool list on the very next request —
// echoing them here would duplicate those bytes permanently in the transcript,
// which is the cost this whole tier exists to avoid.
func renderSearchResult(runnable, blocked []DeferredEntry, activated map[string]bool) string {
	var b strings.Builder
	if len(runnable) > 0 {
		fmt.Fprintf(&b, "Loaded %d tool(s) — callable from your next message:\n", len(runnable))
		for _, m := range runnable {
			fmt.Fprintf(&b, "- %s — %s", m.Name, m.Description)
			if !activated[m.Name] {
				b.WriteString(" (already loaded earlier)")
			}
			b.WriteString("\n")
		}
	}
	if len(blocked) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Matched but unavailable right now:\n")
		for _, m := range blocked {
			fmt.Fprintf(&b, "- %s — %s\n", m.Name, m.Unavailable)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// matchDeferred ranks roster entries against query. A "select:" query returns
// the named entries verbatim; otherwise terms are scored against name and
// description, with "+term" marking a term the name must contain.
func matchDeferred(roster []DeferredEntry, query string, limit int) []DeferredEntry {
	byName := make(map[string]DeferredEntry, len(roster))
	for _, e := range roster {
		byName[e.Name] = e
	}

	if rest, ok := strings.CutPrefix(query, "select:"); ok {
		var out []DeferredEntry
		seen := map[string]bool{}
		for _, name := range strings.Split(rest, ",") {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			if e, ok := byName[name]; ok {
				out = append(out, e)
			}
		}
		return out
	}

	if limit <= 0 {
		limit = defaultSearchResults
	}

	var required, terms []string
	for _, field := range strings.Fields(strings.ToLower(query)) {
		if term, ok := strings.CutPrefix(field, "+"); ok {
			if term != "" {
				required = append(required, term)
			}
			continue
		}
		terms = append(terms, field)
	}

	type scored struct {
		entry DeferredEntry
		score int
		order int
	}
	var hits []scored
	for i, e := range roster {
		name := strings.ToLower(e.Name)
		desc := strings.ToLower(e.Description)

		missing := false
		for _, req := range required {
			if !strings.Contains(name, req) {
				missing = true
				break
			}
		}
		if missing {
			continue
		}

		// A required term that matched is itself evidence, so a bare "+term"
		// query still ranks its hits above nothing.
		score := len(required)
		for _, term := range terms {
			// Name matches weigh more than description matches: callers
			// searching "figma" want the figma tools, not every tool whose
			// description happens to mention it.
			if strings.Contains(name, term) {
				score += 2
			} else if strings.Contains(desc, term) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		hits = append(hits, scored{entry: e, score: score, order: i})
	}

	// Ties break on roster order, which is name-sorted — so the same query
	// against the same roster always loads the same tools.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].order < hits[j].order
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]DeferredEntry, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.entry)
	}
	return out
}
