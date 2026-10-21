package isolation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestPreviousWriterCannotRewriteRegisteredChildDependencies(t *testing.T) {
	binary := os.Getenv("REASONIX_PREVIOUS_ISOLATION_WRITER")
	if binary == "" {
		t.Skip("previous protocol writer binary not supplied")
	}
	repo, managed := gitRepo(t), t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestPreviousIsolationDependencyWriter$")
	cmd.Env = append(os.Environ(), "REASONIX_PREVIOUS_WRITER_REPO="+repo, "REASONIX_PREVIOUS_WRITER_MANAGED="+managed)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	reader := bufio.NewReader(output)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var before durableEntry
	if err := json.Unmarshal(line, &before); err != nil {
		t.Fatalf("previous writer identity: %v %s", err, line)
	}
	if before.Version != 2 || before.State != Running {
		t.Fatalf("not a running previous writer: %s", line)
	}
	s := NewStore(managed, repo, opened(t, repo))
	parent := before.entry()
	child := ChildResult{ID: "iso_12345678", Root: t.TempDir(), Workspace: parent.Cand.WorkspaceRoot, Token: "registered-with-previous-writer-active"}
	if err := s.registerChild(ctx, parent, child); err != nil {
		t.Fatal(err)
	}
	registered, err := os.ReadFile(entryRecordPath(parent))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	line, err = reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(line, &result); err != nil {
		t.Fatalf("previous writer result: %v %s", err, line)
	}
	if result.Code != CodeUnsupportedState {
		t.Fatalf("previous writer overwrote v4: %s", line)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	current, _, err := readLineageRecord(entryRecordPath(parent))
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 4 || current.State != Running || current.Lineage == nil || current.Lineage.Children[child.ID] != child {
		t.Fatalf("previous writer removed dependency barrier: %+v", current)
	}
	after, err := os.ReadFile(entryRecordPath(parent))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(registered, after) {
		t.Fatal("refused previous writer changed registered dependency bytes")
	}
	if _, err := s.Resume(ctx, parent.ID, "owner"); refusalCodeOf(err) != CodeDependentResults {
		t.Fatalf("current reader lost child reservation: %v", err)
	}
}
