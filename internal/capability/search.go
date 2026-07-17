package capability

import (
	"sort"
	"strings"

	"reasonix/internal/retrieval"
)

// SearchKind filters catalog entries for Search.
type SearchKind string

const (
	SearchKindAll   SearchKind = "all"
	SearchKindTool  SearchKind = "tool"
	SearchKindSkill SearchKind = "skill"
	SearchKindMCP   SearchKind = "mcp"
)

// SearchOptions configures a catalog search.
type SearchOptions struct {
	Query string
	// Limit caps results (default 5, max 10). Values outside the range are clamped.
	Limit int
	// Kind is all|tool|skill|mcp (default all).
	Kind SearchKind
}

// SearchHit is one ranked catalog entry.
type SearchHit struct {
	Entry Entry
	Score float64
	// Tier is 0=exact ID/name, 1=prefix, 2=BM25-only. Lower ranks first.
	Tier int
}

const (
	searchTierExact  = 0
	searchTierPrefix = 1
	searchTierBM25   = 2
)

// Search ranks catalog entries for query using exact/prefix preference then
// BM25 over ID, name (weight 3), source, description (weight 2), triggers, and
// requires. Disabled entries are skipped. Tie-break is capability ID.
// Search never starts processes or contacts the network; it only reads entries.
func Search(entries []Entry, opts SearchOptions) []SearchHit {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	kind := opts.Kind
	if kind == "" {
		kind = SearchKindAll
	}

	queryNorm := strings.ToLower(query)
	queryTerms, err := retrieval.QueryTerms(query)
	if err != nil {
		// Non-token queries can still exact/prefix match IDs/names (e.g. punctuation-only
		// is empty; bare symbols fall through to nil).
		queryTerms = nil
	}

	type doc struct {
		entry  Entry
		counts map[string]int
		length int
		tier   int
	}

	filtered := make([]doc, 0, len(entries))
	for _, e := range entries {
		if e.Status == StatusDisabled {
			continue
		}
		if !matchSearchKind(e.Kind, kind) {
			continue
		}
		idNorm := strings.ToLower(strings.TrimSpace(e.ID))
		nameNorm := strings.ToLower(strings.TrimSpace(e.Name))
		tier := searchTierBM25
		switch {
		case idNorm == queryNorm || nameNorm == queryNorm:
			tier = searchTierExact
		case idNorm != "" && (strings.HasPrefix(idNorm, queryNorm) || strings.HasPrefix(queryNorm, idNorm)):
			tier = searchTierPrefix
		case nameNorm != "" && (strings.HasPrefix(nameNorm, queryNorm) || strings.HasPrefix(queryNorm, nameNorm)):
			tier = searchTierPrefix
		}
		text := indexText(e)
		terms := retrieval.Tokens(text)
		counts := retrieval.Counts(terms)
		filtered = append(filtered, doc{
			entry:  e,
			counts: counts,
			length: len(terms),
			tier:   tier,
		})
	}
	if len(filtered) == 0 {
		return nil
	}

	// BM25 corpus over the filtered set (including exact/prefix candidates).
	countMaps := make([]map[string]int, len(filtered))
	var totalLen int
	for i, d := range filtered {
		countMaps[i] = d.counts
		totalLen += d.length
	}
	df := retrieval.DocumentFrequency(countMaps)
	avgLen := 1.0
	if len(filtered) > 0 {
		avgLen = float64(totalLen) / float64(len(filtered))
	}
	if avgLen <= 0 {
		avgLen = 1
	}

	hits := make([]SearchHit, 0, len(filtered))
	for _, d := range filtered {
		score := 0.0
		if len(queryTerms) > 0 {
			score = retrieval.BM25Score(d.counts, d.length, queryTerms, df, len(filtered), avgLen)
		}
		// Exact/prefix always surface; BM25-only needs a positive score.
		if d.tier == searchTierBM25 && score <= 0 {
			continue
		}
		// Boost score slightly by tier so result payloads still reflect rank quality
		// without overturning the primary tier sort.
		hits = append(hits, SearchHit{Entry: d.entry, Score: score, Tier: d.tier})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Tier != hits[j].Tier {
			return hits[i].Tier < hits[j].Tier
		}
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Entry.ID < hits[j].Entry.ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func matchSearchKind(k Kind, filter SearchKind) bool {
	switch filter {
	case SearchKindAll, "":
		return true
	case SearchKindTool:
		return k == KindTool
	case SearchKindSkill:
		return k == KindSkill
	case SearchKindMCP:
		return k == KindMCPServer || k == KindMCPTool
	default:
		return false
	}
}

// indexText builds the BM25 document for an entry. Name is weighted 3× and
// description 2×; remaining fields contribute once.
func indexText(e Entry) string {
	var b strings.Builder
	writeN := func(s string, n int) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for i := 0; i < n; i++ {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(s)
		}
	}
	writeN(e.Name, 3)
	writeN(e.Description, 2)
	writeN(e.ID, 1)
	writeN(e.Source, 1)
	for _, t := range e.Triggers {
		writeN(t, 1)
	}
	for _, r := range e.Requires {
		writeN(r, 1)
	}
	return b.String()
}

// ParseSearchKind normalizes a kind filter string.
func ParseSearchKind(raw string) SearchKind {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return SearchKindAll
	case "tool":
		return SearchKindTool
	case "skill":
		return SearchKindSkill
	case "mcp":
		return SearchKindMCP
	default:
		return SearchKind(strings.ToLower(strings.TrimSpace(raw)))
	}
}
