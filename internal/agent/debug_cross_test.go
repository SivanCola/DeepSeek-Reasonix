package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func TestDebugCrossProc(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jsonl")
	msgs := []provider.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
	}
	if err := (&Session{Messages: msgs}).Save(src); err != nil {
		t.Fatal(err)
	}
	unlock := lockSessionSavePath(src)
	unlockFile, err := lockSessionFile(src)
	if err != nil {
		t.Fatal(err)
	}
	msgs3 := append(append([]provider.Message(nil), msgs...), provider.Message{Role: "assistant", Content: "third"})
	digest, _, err := digestAndSizeSessionMessages(msgs3)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendSessionAppendEvent(src, 2, msgs3[2:], digest, 1); err != nil {
		t.Fatal(err)
	}
	unlockFile()
	unlock()
	b, _ := os.ReadFile(store.SessionEventLog(src))
	fmt.Printf("event log: %s\n", string(b))
	probe, _ := probeSessionEventLog(src)
	fmt.Printf("probe: native=%v size=%d\n", probe.native, probe.size)
	loaded, err := LoadSession(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range loaded.Messages {
		fmt.Printf("loaded: %s\n", m.Content)
	}
}
