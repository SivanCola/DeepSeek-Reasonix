package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/session/control"
	"reasonix/internal/state/sessionstore"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

type fleetRecoveryProvider struct {
	mu            sync.Mutex
	id            string
	first, second int
	result        string
	parents       []provider.Request
}

func (*fleetRecoveryProvider) Name() string { return "fleet-recovery" }
func (p *fleetRecoveryProvider) Stream(_ context.Context, r provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan provider.Chunk, 2)
	call := func(id, name string, args any) {
		raw, _ := json.Marshal(args)
		ch <- provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Arguments: string(raw)}}
	}
	first := firstUser(r)
	if strings.Contains(first, "SECOND-FLEET-CHILD") {
		p.second++
		if p.second == 1 {
			return nil, fmt.Errorf("fixture failure before completion")
		}
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "second complete"}
	} else if strings.Contains(first, "FIRST-FLEET-CHILD") {
		p.first++
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "first complete"}
	} else {
		p.parents = append(p.parents, r)
		if toolResultAfterLastUser(r) {
			p.result = lastToolResult(r)
			ch <- provider.Chunk{Type: provider.ChunkText, Text: "parent complete"}
		} else if p.id != "" {
			call("resume", "fleet", map[string]any{"recovery": map[string]string{"action": "resume", "id": p.id}})
		} else {
			call("initial", "fleet", map[string]any{"tasks": []map[string]any{
				{"id": "one", "prompt": "FIRST-FLEET-CHILD", "read_only": true, "tools": []string{"read_file"}},
				{"id": "two", "prompt": "SECOND-FLEET-CHILD", "depends_on": []string{"one"}, "read_only": true, "tools": []string{"read_file"}},
			}})
		}
	}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func TestEffectFleetRecoveryAfterRestartReusesCompletedAnswer(t *testing.T) {
	home, dir := statedBootHome(t), robustTempDir(t)
	p := &fleetRecoveryProvider{}
	kind := uniqueKind("fleet-recovery")
	provider.Register(kind, func(provider.Config) (provider.Provider, error) { return p, nil })
	writeFile(t, dir, "reasonix.toml", `default_model = "probe"
[codegraph]
enabled = false
[[providers]]
name = "probe"
kind = "`+kind+`"
model = "probe"
`)
	mirrorToUserConfigAt(t, home, dir)
	build := func() *control.Controller {
		c, err := Build(context.Background(), Options{Home: home, WorkspaceRoot: dir, Sink: event.Discard})
		if err != nil {
			t.Fatal(err)
		}
		c.SetToolApprovalMode(control.ToolApprovalYolo)
		return c
	}
	c := build()
	c.EnsureSessionPath()
	if err := c.Run(context.Background(), "Run the fleet"); err != nil {
		t.Fatal(err)
	}
	p.id = regexp.MustCompile(`fr_[A-Za-z0-9]+`).FindString(p.result)
	if p.id == "" || p.first != 1 || p.second != 1 {
		t.Fatalf("before restart: first=%d second=%d result=%s", p.first, p.second, p.result)
	}
	path := c.SessionPath()
	c.Close()
	sess, err := sessionstore.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	c = build()
	defer c.Close()
	if err := c.Resume(sess, path); err != nil {
		t.Fatal(err)
	}
	if p.first != 1 || p.second != 1 {
		t.Fatal("startup replayed work")
	}
	if err := c.Run(context.Background(), "Explicitly resume unfinished fleet work"); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.first != 1 || p.second != 2 || !strings.Contains(p.result, "Completed fleet") {
		t.Fatalf("after recovery: first=%d second=%d result=%s", p.first, p.second, p.result)
	}
	if len(p.parents) != 4 || !reflect.DeepEqual(p.parents[0].Tools, p.parents[2].Tools) {
		t.Fatal("recovery state changed tool prefix")
	}
}
