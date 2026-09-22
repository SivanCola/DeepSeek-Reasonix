package sessioncatalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// OrdinaryPage is a flat session projection. A topic containing many sessions
// must not expand every member to return the first page of the sidebar.
type OrdinaryPageRequest struct {
	Scope, WorkspaceRoot, Cursor, SortMode, ExcludedPathsJSON string
	Limit                                                     int
	MinActivity                                               int64
	PinnedOnly, ExcludePinned                                 bool
}

type OrdinaryRecord struct {
	SessionRecord
	Title, TitleSource string
	Pinned             bool
	Cursor             string
}

type ordinaryCursor struct {
	Pinned   int    `json:"p"`
	Activity int64  `json:"a"`
	Topic    string `json:"t"`
	Path     string `json:"s"`
}

func (c *Catalog) MetadataOnly() bool { return c.opts.MetadataOnly }

// Multi-head projections retain the existing branch-aware adapter until each
// head is independently represented in the flat catalog view.
func (c *Catalog) HasMultipleHeads(ctx context.Context, scope, root string) (bool, error) {
	var found bool
	err := c.readDB(ctx).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_sessions WHERE scope=? AND workspace_root_key=? AND head_count>1)`, scope, c.workspaceRootKey(scope, root)).Scan(&found)
	return found, err
}

func (c *Catalog) HasTopicSessions(ctx context.Context, scope, root, topic string) (bool, error) {
	var found bool
	err := c.readDB(ctx).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_sessions WHERE scope=? AND workspace_root_key=? AND topic_id=?)`, scope, c.workspaceRootKey(scope, root), topic).Scan(&found)
	return found, err
}

func (c *Catalog) ListOrdinarySessions(ctx context.Context, req OrdinaryPageRequest) ([]OrdinaryRecord, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	activity := `COALESCE(NULLIF(s.last_activity_at,0),s.created_at)`
	index := `idx_catalog_sessions_flat_activity`
	if req.SortMode == "created" {
		activity = `COALESCE(NULLIF(s.created_at,0),s.last_activity_at)`
		index = `idx_catalog_sessions_flat_created`
	}
	where := `s.scope=? AND s.workspace_root_key=? AND s.missing_since=0 AND s.health<>'missing' AND s.ordinary_visible=1`
	args := []any{req.Scope, c.workspaceRootKey(req.Scope, req.WorkspaceRoot)}
	if req.MinActivity > 0 {
		where += ` AND max(s.created_at,s.last_activity_at)>=?`
		args = append(args, req.MinActivity)
	}
	if req.PinnedOnly {
		where += ` AND s.topic_pinned=1`
	}
	if req.ExcludePinned {
		where += ` AND s.topic_pinned=0`
	}
	if req.ExcludedPathsJSON != "" {
		where += ` AND s.path NOT IN (SELECT value FROM json_each(?))`
		args = append(args, req.ExcludedPathsJSON)
	}
	var cursor *ordinaryCursor
	if req.Cursor != "" {
		var cur ordinaryCursor
		b, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(b, &cur) != nil {
			return nil, fmt.Errorf("invalid ordinary cursor")
		}
		cursor = &cur
	}
	columns := "s." + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(sessionSelectColumns, "\n", ""), " ", ""), ",", ",s.")
	selectSQL := `SELECT ` + columns + `,t.title,t.title_source,s.topic_pinned AS page_pin,` + activity + ` AS page_activity FROM catalog_sessions s INDEXED BY ` + index + ` JOIN catalog_topics t ON t.scope=s.scope AND t.workspace_root_key=s.workspace_root_key AND t.topic_id=s.topic_id WHERE ` + where
	orderSQL := ` ORDER BY s.topic_pinned DESC,` + activity + ` DESC,s.topic_id,s.path LIMIT ?`
	query := selectSQL + orderSQL
	if cursor == nil {
		args = append(args, limit)
	} else {
		// Mixed-direction tuples expressed with negation cannot seek our
		// descending index. Split the disjoint suffix into three bounded
		// index ranges, then merge at most 3*limit rows. Deep pages must not
		// walk every preceding page to locate their first row.
		cur := *cursor
		ranges := []struct {
			predicate string
			values    []any
		}{
			{`s.topic_pinned=? AND ` + activity + `=? AND (s.topic_id,s.path)>(?,?)`, []any{cur.Pinned, cur.Activity, cur.Topic, cur.Path}},
			{`s.topic_pinned=? AND ` + activity + `<?`, []any{cur.Pinned, cur.Activity}},
			{`s.topic_pinned<?`, []any{cur.Pinned}},
		}
		parts := make([]string, 0, len(ranges))
		baseArgs := args
		args = nil
		for _, span := range ranges {
			parts = append(parts, `SELECT * FROM (`+selectSQL+` AND `+span.predicate+orderSQL+`)`)
			args = append(args, baseArgs...)
			args = append(args, span.values...)
			args = append(args, limit)
		}
		query = `SELECT * FROM (` + strings.Join(parts, ` UNION ALL `) + `) ORDER BY page_pin DESC,page_activity DESC,topic_id,path LIMIT ?`
		args = append(args, limit)
	}
	rows, err := c.readDB(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OrdinaryRecord{}
	for rows.Next() {
		var record OrdinaryRecord
		var pin int
		var value int64
		scanner := appendedScanner{rows, []any{&record.Title, &record.TitleSource, &pin, &value}}
		record.SessionRecord, err = scanSession(scanner)
		if err != nil {
			return nil, err
		}
		if c.pathRemovedKey(record.pathKey, record.Path) {
			return nil, fmt.Errorf("catalog snapshot source removed")
		}
		record.Pinned = pin != 0
		b, _ := json.Marshal(ordinaryCursor{pin, value, record.TopicID, record.Path})
		record.Cursor = base64.RawURLEncoding.EncodeToString(b)
		out = append(out, record)
	}
	return out, rows.Err()
}

type appendedScanner struct {
	row  interface{ Scan(...any) error }
	tail []any
}

func (s appendedScanner) Scan(args ...any) error { return s.row.Scan(append(args, s.tail...)...) }
