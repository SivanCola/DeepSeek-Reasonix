package permission

import (
	"encoding/json"
	"testing"
)

func TestHashlinePermissionAliasesMatchClassicRules(t *testing.T) {
	// deny edit_file must block hashline_edit (anchor ops).
	denyEdit := New("allow", nil, nil, []string{"edit_file"})
	if got := denyEdit.Decide("hashline_edit", false, json.RawMessage(`{"path":"a.go","edits":{"op":"replace","anchor":"1:abc:def","content":"x"}}`)); got != Deny {
		t.Fatalf("deny edit_file vs hashline_edit = %v, want Deny", got)
	}
	// deny hashline_edit must still block sole-write (identity set keeps original name).
	denyHL := New("allow", nil, nil, []string{"hashline_edit"})
	if got := denyHL.Decide("hashline_edit", false, json.RawMessage(`{"path":"a.go","edits":{"op":"write","content":"whole"}}`)); got != Deny {
		t.Fatalf("deny hashline_edit vs sole-write = %v, want Deny", got)
	}
	// deny write_file must block sole write via hashline_edit.
	denyWrite := New("allow", nil, nil, []string{"write_file"})
	if got := denyWrite.Decide("hashline_edit", false, json.RawMessage(`{"path":"a.go","edits":{"op":"write","content":"whole"}}`)); got != Deny {
		t.Fatalf("deny write_file vs hashline write = %v, want Deny", got)
	}
	// deny edit_file must NOT block sole-write (write semantics, not edit).
	if got := denyEdit.Decide("hashline_edit", false, json.RawMessage(`{"path":"a.go","edits":{"op":"write","content":"whole"}}`)); got != Allow {
		t.Fatalf("deny edit_file vs sole-write = %v, want Allow (write maps to write_file only)", got)
	}
	// deny read_file must block hashline_read.
	denyRead := New("allow", nil, nil, []string{"read_file"})
	if got := denyRead.Decide("hashline_read", true, json.RawMessage(`{"path":"a.go"}`)); got != Deny {
		t.Fatalf("deny read_file vs hashline_read = %v, want Deny", got)
	}
	// deny grep must block hashline_grep.
	denyGrep := New("allow", nil, nil, []string{"grep"})
	if got := denyGrep.Decide("hashline_grep", true, json.RawMessage(`{"pattern":"x"}`)); got != Deny {
		t.Fatalf("deny grep vs hashline_grep = %v, want Deny", got)
	}
	// ask edit_file must ask for hashline_edit under mode=allow.
	askEdit := New("allow", nil, []string{"edit_file"}, nil)
	if got := askEdit.Decide("hashline_edit", false, json.RawMessage(`{"path":"a.go","edits":{"op":"replace","anchor":"1:abc:def","content":"x"}}`)); got != Ask {
		t.Fatalf("ask edit_file vs hashline_edit = %v, want Ask", got)
	}
	// allow edit_file under mode=ask covers hashline_edit.
	allowEdit := New("ask", []string{"edit_file"}, nil, nil)
	if got := allowEdit.Decide("hashline_edit", false, json.RawMessage(`{"path":"a.go","edits":{"op":"replace","anchor":"1:abc:def","content":"x"}}`)); got != Allow {
		t.Fatalf("allow edit_file vs hashline_edit = %v, want Allow", got)
	}
	// file_mutation still covers hashline_edit.
	denyMut := New("allow", nil, nil, []string{"file_mutation"})
	if got := denyMut.Decide("hashline_edit", false, json.RawMessage(`{"path":"a.go","edits":{"op":"replace","anchor":"1:abc:def","content":"x"}}`)); got != Deny {
		t.Fatalf("deny file_mutation vs hashline_edit = %v, want Deny", got)
	}
}

func TestPermissionIdentity(t *testing.T) {
	if got := PermissionIdentity("hashline_read"); got != "read_file" {
		t.Fatalf("hashline_read → %q", got)
	}
	if got := PermissionIdentity("hashline_edit"); got != "edit_file" {
		t.Fatalf("hashline_edit → %q", got)
	}
	if got := PermissionIdentity("hashline_grep"); got != "grep" {
		t.Fatalf("hashline_grep → %q", got)
	}
	if got := PermissionIdentity("bash"); got != "bash" {
		t.Fatalf("bash → %q", got)
	}
}

func TestHashlineSoleWriteDenyHashlineEdit(t *testing.T) {
	denyHL := New("allow", nil, nil, []string{"hashline_edit"})
	if got := denyHL.Decide("hashline_edit", false, json.RawMessage(`{"path":"a.go","edits":{"op":"write","content":"whole"}}`)); got != Deny {
		t.Fatalf("deny hashline_edit vs sole write = %v, want Deny", got)
	}
}
