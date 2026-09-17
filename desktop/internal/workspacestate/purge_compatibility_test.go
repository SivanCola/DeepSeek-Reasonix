package workspacestate

import (
	"encoding/json"
	"os"
	"testing"

	previous "reasonix/desktop/internal/workspacestate/testdata/v2previous"
)

func TestPurgePreviousV2ReaderAndUnrelatedWrite(t *testing.T) {
	for _, phase := range []string{"prepared", "tombstoned", "content_removed", "committed"} {
		t.Run(phase, func(t *testing.T) {
			store, expected := seedArchivedProcessState(t)
			old := previous.NewStore(store.Path())
			// Produce a real legacy prepare using the previous implementation.
			if err := old.BeginPurge(t.Context(), "victim", expected); err != nil {
				t.Fatal(err)
			}
			if err := store.mutate(t.Context(), func(s *State) error {
				op := s.PendingOperations["purge-victim"]
				op.extra = map[string]json.RawMessage{"future": json.RawMessage(`{"nested":[1,2]}`)}
				s.PendingOperations[op.ID] = op
				s.extra = map[string]json.RawMessage{"futureRoot": json.RawMessage(`{"keep":true}`)}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			state, err := store.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if phase != "prepared" {
				if err := store.ResumePurge(t.Context(), "victim", state.PendingOperations["purge-victim"]); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "content_removed" || phase == "committed" {
				if err := store.AdvancePurge(t.Context(), "victim", "content_removed"); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "committed" {
				if err := store.CompletePurge(t.Context(), "victim"); err != nil {
					t.Fatal(err)
				}
			}
			state, err = store.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(state.PendingOperations["purge-victim"])
			read, err := old.Load(t.Context())
			if err != nil || read.PendingOperations["purge-victim"].Phase != phase {
				t.Fatalf("previous reader phase=%s err=%v", phase, err)
			}
			if err := old.RenameWorkspace(t.Context(), GlobalWorkspaceID, "Unrelated title"); err != nil {
				t.Fatal(err)
			}
			state, err = store.Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(state.PendingOperations["purge-victim"])
			if string(got) != string(want) || string(state.extra["futureRoot"]) != `{"keep":true}` {
				t.Fatalf("previous writer dropped evidence: %s => %s", want, got)
			}
			if phase == "committed" {
				before, _ := os.ReadFile(store.Path())
				if err := store.CompletePurge(t.Context(), "victim"); err != nil {
					t.Fatal(err)
				}
				after, _ := os.ReadFile(store.Path())
				if string(before) != string(after) {
					t.Fatal("idempotent completion rewrote registry")
				}
			}
		})
	}
}
