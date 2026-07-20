package protocol

import "testing"

func TestHelloCompatibleRequiresMajorAndCapabilities(t *testing.T) {
	ok := HelloResponse{
		ProtocolMajor: ProtocolMajor,
		ProtocolMinor: ProtocolMinor,
		Workspace:     "/w",
		Capabilities:  RequiredCapabilities,
	}
	if err := ok.Compatible(); err != nil {
		t.Fatal(err)
	}
	badMajor := ok
	badMajor.ProtocolMajor = ProtocolMajor + 1
	if err := badMajor.Compatible(); err == nil {
		t.Fatal("expected major mismatch")
	}
	badCaps := ok
	badCaps.Capabilities = CapSessions
	if err := badCaps.Compatible(); err == nil {
		t.Fatal("expected capability mismatch")
	}
	emptyWS := ok
	emptyWS.Workspace = ""
	if err := emptyWS.Compatible(); err == nil {
		t.Fatal("expected empty workspace error")
	}
}

func TestErrorBody(t *testing.T) {
	e := NewError(CodeUnauthorized, "nope")
	if e.Error() != CodeUnauthorized+": nope" {
		t.Fatalf("error = %q", e.Error())
	}
}
