package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"reasonix/internal/provider"
)

const (
	// maxCompactionStateBytes is the hard cap for the deterministic state block.
	maxCompactionStateBytes = 12 * 1024
	maxCompactionTodos      = 32
	maxCompactionJobs       = 16
	maxCompactionPaths      = 64
)

// CompactionState is a deterministic snapshot the Controller supplies so
// post-compaction recovery never relies on the model summarizing Goals/Todos.
type CompactionState struct {
	ActiveGoal     string
	BlockedGoal    string
	GoalStatus     string
	BlockReason    string
	Todos          []CompactionTodo // pending + in_progress, capped
	CompletedTodos int
	CancelledTodos int
	Jobs           []CompactionJob // running jobs/sub-agents, capped
	EditedPaths    []string        // sorted union, capped
	DeliveryCP     string          // delivery checkpoint summary
	ArchivePath    string
}

// CompactionTodo is a compact todo line for state recovery.
type CompactionTodo struct {
	ID     string
	Status string
	Text   string
}

// CompactionJob is a running background job or sub-agent.
type CompactionJob struct {
	ID    string
	Kind  string
	Label string
}

// CompactionStateProvider is implemented by the Controller to supply state at
// compact time without importing control into agent cycles.
type CompactionStateProvider interface {
	CompactionState() CompactionState
}

// RenderCompactionState formats CompactionState deterministically. Over-limit
// content is truncated in fixed order and closed with a SHA-256 digest.
func RenderCompactionState(st CompactionState) string {
	var b strings.Builder
	b.WriteString("<compaction-state version=\"1\">\n")

	writeKV(&b, "active_goal", st.ActiveGoal)
	writeKV(&b, "blocked_goal", st.BlockedGoal)
	writeKV(&b, "goal_status", st.GoalStatus)
	writeKV(&b, "block_reason", st.BlockReason)

	b.WriteString("## Todos\n")
	todos := st.Todos
	if len(todos) > maxCompactionTodos {
		todos = todos[:maxCompactionTodos]
	}
	for _, t := range todos {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", t.Status, t.ID, singleLine(t.Text))
	}
	fmt.Fprintf(&b, "completed_count: %d\n", st.CompletedTodos)
	fmt.Fprintf(&b, "cancelled_count: %d\n", st.CancelledTodos)

	b.WriteString("## Jobs\n")
	jobs := st.Jobs
	if len(jobs) > maxCompactionJobs {
		jobs = jobs[:maxCompactionJobs]
	}
	for _, j := range jobs {
		fmt.Fprintf(&b, "- %s/%s: %s\n", j.Kind, j.ID, singleLine(j.Label))
	}

	b.WriteString("## Edited paths\n")
	paths := append([]string(nil), st.EditedPaths...)
	sort.Strings(paths)
	if len(paths) > maxCompactionPaths {
		paths = paths[:maxCompactionPaths]
	}
	for _, p := range paths {
		fmt.Fprintf(&b, "- %s\n", p)
	}

	writeKV(&b, "delivery_checkpoint", st.DeliveryCP)
	writeKV(&b, "archive_path", st.ArchivePath)
	b.WriteString("</compaction-state>\n")

	out := b.String()
	if len(out) <= maxCompactionStateBytes {
		return out
	}
	// Truncate body, keep tags, append digest of the full intended content.
	sum := sha256.Sum256([]byte(out))
	digest := hex.EncodeToString(sum[:])
	keep := maxCompactionStateBytes - 96
	if keep < 128 {
		keep = 128
	}
	truncated := out[:keep]
	// Ensure we don't cut mid-line awkwardly
	if i := strings.LastIndexByte(truncated, '\n'); i > 64 {
		truncated = truncated[:i+1]
	}
	return truncated + fmt.Sprintf("… truncated; sha256=%s\n</compaction-state>\n", digest)
}

func writeKV(b *strings.Builder, k, v string) {
	v = singleLine(v)
	if v == "" {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", k, v)
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// CompactionStateMessage builds the synthetic state message.
func CompactionStateMessage(st CompactionState) provider.Message {
	return SyntheticUser(provider.SyntheticCompactionState, RenderCompactionState(st))
}

// CompactionSummaryMessage wraps a model or mechanical summary with metadata.
func CompactionSummaryMessage(content string) provider.Message {
	body := content
	if !strings.Contains(content, summaryTagOpen) {
		body = summaryTagOpen + "\n" + strings.TrimSpace(content) + "\n" + summaryTagClose
	}
	return SyntheticUser(provider.SyntheticCompactionSummary, body)
}
