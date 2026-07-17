package agent

import "testing"

func TestIsCrossProtocolToolAcceptsShortAndWireForms(t *testing.T) {
	// Wire form
	if !IsCrossProtocolTool(EditProtocolHashline, "read_file") {
		t.Fatal("hashline-v1 must reject classic read_file")
	}
	if !IsCrossProtocolTool(EditProtocolClassic, "hashline_edit") {
		t.Fatal("classic-v1 must reject hashline_edit")
	}
	// Short CLI form
	if !IsCrossProtocolTool("hashline", "write_file") {
		t.Fatal("hashline must reject write_file")
	}
	if !IsCrossProtocolTool("classic", "hashline_read") {
		t.Fatal("classic must reject hashline_read")
	}
	// Empty skips filtering
	if IsCrossProtocolTool("", "hashline_edit") {
		t.Fatal("empty protocol must not filter")
	}
	// Same-protocol tools are allowed
	if IsCrossProtocolTool(EditProtocolHashline, "hashline_edit") {
		t.Fatal("hashline must allow hashline_edit")
	}
	if IsCrossProtocolTool(EditProtocolClassic, "edit_file") {
		t.Fatal("classic must allow edit_file")
	}
}
