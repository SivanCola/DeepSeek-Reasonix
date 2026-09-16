package main

import (
	"context"
	"reflect"
	"testing"

	"reasonix/desktop/internal/workspacestate"
	"reasonix/internal/session"
)

type workspaceInfoProbe struct{ ids []string }

func (p *workspaceInfoProbe) Stat(_ context.Context, ref session.SessionRef) (session.SessionInfo, error) {
	p.ids = append(p.ids, ref.SessionID)
	return session.SessionInfo{SessionID: ref.SessionID, MetadataStatus: session.MetadataReady}, nil
}

func TestWorkspaceMetadataReadsOnlyRegisteredMembers(t *testing.T) {
	probe := &workspaceInfoProbe{}
	for _, ids := range [][]string{{"a", "b", "a"}, {"c"}, {"d", "e"}} {
		if _, err := listWorkspaceSessionInfo(t.Context(), probe, ids); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(probe.ids, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("repeated or unrelated reads: %v", probe.ids)
	}
}

func TestWorkspacePendingMetadataIsNotBlank(t *testing.T) {
	for _, status := range []string{session.MetadataPending, session.MetadataFailed} {
		row := workspaceSessionRow("global", "old", session.SessionInfo{MetadataStatus: status}, true, false, nil)
		if row.Blank {
			t.Fatalf("%s metadata was classified as blank", status)
		}
	}
	row := workspaceSessionRow("global", "missing", session.SessionInfo{}, false, false, nil)
	if row.Blank {
		t.Fatal("missing metadata was classified as blank")
	}
	row = workspaceSessionRow("global", "empty", session.SessionInfo{MetadataStatus: session.MetadataReady}, true, false, nil)
	if !row.Blank {
		t.Fatal("ready empty session should be blank")
	}
	row = workspaceSessionRow("global", "named", session.SessionInfo{MetadataStatus: session.MetadataReady, Title: "Saved title"}, true, false, nil)
	if row.Blank {
		t.Fatal("named session should not be hidden")
	}
}

func TestCanonicalSessionTopicIdentityUsesTargetPresentation(t *testing.T) {
	state := workspacestate.State{Presentation: map[string]workspacestate.Presentation{
		"target": {TopicID: "topic-target", Title: "Target"},
	}}
	topicID, title := canonicalSessionTopicIdentity(state, "target")
	if topicID != "topic-target" || title != "Target" {
		t.Fatalf("identity = %q/%q, want target presentation", topicID, title)
	}
	topicID, title = canonicalSessionTopicIdentity(state, "missing")
	if topicID != "canonical-missing" || title != "" {
		t.Fatalf("fallback identity = %q/%q", topicID, title)
	}
}
