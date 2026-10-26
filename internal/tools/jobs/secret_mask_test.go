package jobs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/base/secrets/secretstest"
	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
)

// A background job's log sits in the sessions folder, where other commands can
// read it: what it keeps is masked as it is written, a value split across two
// writes included, and so is the result a job returns.
func TestABackgroundJobKeepsItsOutputMasked(t *testing.T) {
	const value = "sk-job-log-0123456789"
	secretstest.Hold("JOB_KEY", value)
	sessionPath := filepath.Join(testenv.TempDir(t), "session.jsonl")
	m := NewManager(event.Discard)
	defer m.Close()
	m.SetActiveSessionPath("session", sessionPath)
	j := m.StartForSession("session", "bash", "server", func(_ context.Context, out io.Writer) (string, error) {
		_, _ = io.WriteString(out, "config key=sk-job-l")
		_, _ = io.WriteString(out, "og-0123456789 loaded\n")
		return "finished with " + value, nil
	})
	<-j.done
	j.mu.Lock()
	path := j.artifact.path
	j.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-job") || !strings.Contains(string(data), "key=[secret:JOB_KEY] loaded") || !strings.Contains(string(data), "finished with [secret:JOB_KEY]") {
		t.Fatalf("log on disk = %q", data)
	}
}
