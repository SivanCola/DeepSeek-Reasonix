package ccswitch

import (
	"database/sql"
	"testing"
)

func mustOpenMem(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE mcp_servers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		server_config TEXT NOT NULL,
		description TEXT,
		homepage TEXT,
		docs TEXT,
		tags TEXT NOT NULL DEFAULT '[]',
		enabled_claude BOOLEAN NOT NULL DEFAULT 0,
		enabled_codex BOOLEAN NOT NULL DEFAULT 0,
		enabled_gemini BOOLEAN NOT NULL DEFAULT 0,
		enabled_opencode BOOLEAN NOT NULL DEFAULT 0,
		enabled_hermes BOOLEAN NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedServers(t *testing.T, db *sql.DB, rows ...[3]string) {
	t.Helper()
	for _, r := range rows {
		_, err := db.Exec(`INSERT OR REPLACE INTO mcp_servers (id, name, server_config)
			VALUES (?, ?, ?)`, r[0], r[1], r[2])
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestImportStdio(t *testing.T) {
	db := mustOpenMem(t)
	seedServers(t, db,
		[3]string{"filesystem", "filesystem",
			`{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"],"env":{"HOME":"/home/user"}}`},
		[3]string{"fetch", "fetch",
			`{"command":"uvx","args":["mcp-server-fetch"]}`},
	)
	rows, err := db.Query(`SELECT name, server_config FROM mcp_servers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	entries, err := parseRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	e := entries[0]
	if e.Name != "fetch" || e.Type != "stdio" || e.Command != "uvx" {
		t.Errorf("stdio entry mismatch: %+v", e)
	}
	e = entries[1]
	if e.Name != "filesystem" || len(e.Args) != 3 || len(e.Env) != 1 {
		t.Errorf("stdio entry with env mismatch: %+v", e)
	}
}

func TestImportHTTP(t *testing.T) {
	db := mustOpenMem(t)
	seedServers(t, db,
		[3]string{"web", "web",
			`{"type":"http","url":"https://api.example.com/mcp","headers":{"Authorization":"Bearer tok"}}`},
	)
	rows, err := db.Query(`SELECT name, server_config FROM mcp_servers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	entries, err := parseRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Name != "web" || e.Type != "http" || e.URL != "https://api.example.com/mcp" {
		t.Errorf("http entry mismatch: %+v", e)
	}
	if e.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("headers mismatch: %v", e.Headers)
	}
}

func TestImportSSE(t *testing.T) {
	db := mustOpenMem(t)
	seedServers(t, db,
		[3]string{"remote", "remote",
			`{"type":"sse","url":"https://sse.example.com/mcp","headers":{"x-api-key":"k1"}}`},
	)
	rows, err := db.Query(`SELECT name, server_config FROM mcp_servers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	entries, err := parseRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	e := entries[0]
	if e.Name != "remote" || e.Type != "sse" || e.URL != "https://sse.example.com/mcp" {
		t.Errorf("sse entry mismatch: %+v", e)
	}
}

func TestImportDedup(t *testing.T) {
	db := mustOpenMem(t)
	seedServers(t, db,
		[3]string{"a", "same-name",
			`{"command":"first"}`},
		[3]string{"b", "same-name",
			`{"command":"second"}`},
	)
	rows, err := db.Query(`SELECT name, server_config FROM mcp_servers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	entries, err := parseRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated entry, got %d", len(entries))
	}
	if entries[0].Command != "first" {
		t.Errorf("first entry should win: %+v", entries[0])
	}
}

func TestImportInvalidJSON(t *testing.T) {
	db := mustOpenMem(t)
	seedServers(t, db,
		[3]string{"bad", "bad", `not json`},
		[3]string{"good", "good", `{"command":"ok"}`},
	)
	rows, err := db.Query(`SELECT name, server_config FROM mcp_servers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	entries, err := parseRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 valid entry (bad JSON skipped), got %d", len(entries))
	}
	if entries[0].Name != "good" {
		t.Errorf("expected 'good' entry, got %q", entries[0].Name)
	}
}

func TestToPluginEntryTypeDefault(t *testing.T) {
	// Empty type field defaults to stdio.
	e, err := toPluginEntry("test", `{"command":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if e.Type != "stdio" {
		t.Errorf("expected implicit stdio, got %q", e.Type)
	}
}

func TestImportEmptyDB(t *testing.T) {
	db := mustOpenMem(t)
	rows, err := db.Query(`SELECT name, server_config FROM mcp_servers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	entries, err := parseRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries from empty table, got %d", len(entries))
	}
}
