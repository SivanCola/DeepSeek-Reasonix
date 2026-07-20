package target

import "testing"

func TestManagerStartsLocalAndFencesSwitch(t *testing.T) {
	m := New()
	id, gen, seq := m.Active()
	if id.Kind != KindLocal || gen == 0 {
		t.Fatalf("active = %+v gen=%d", id, gen)
	}

	remoteID, rgen, err := m.BeginRemoteConnect("lab", "/home/u/w")
	if err != nil {
		t.Fatal(err)
	}
	if remoteID.HostID != "lab" {
		t.Fatalf("remote = %+v", remoteID)
	}
	if err := m.MarkRemoteConnected(rgen); err != nil {
		t.Fatal(err)
	}
	active, igen, rseq, err := m.ActivateRemote(rgen)
	if err != nil {
		t.Fatal(err)
	}
	if active.Kind != KindRemote {
		t.Fatalf("active = %+v", active)
	}
	// Old tokens are stale after activate.
	if !m.IsStale(gen, seq) {
		t.Fatal("expected pre-switch tokens to be stale")
	}
	if m.IsStale(igen, rseq) {
		t.Fatal("current tokens should not be stale")
	}

	// Switch back to local while remote stays connected in background.
	m.SwitchLocal()
	bg := m.RemoteBackground()
	if bg == nil || !bg.Connected {
		t.Fatalf("background remote = %+v", bg)
	}
	hint := m.LastRemoteHint()
	if hint.HostID != "lab" || hint.Workspace != "/home/u/w" {
		t.Fatalf("hint = %+v", hint)
	}
}

func TestManagerRejectsBusyHostSwap(t *testing.T) {
	m := New()
	_, gen, err := m.BeginRemoteConnect("a", "/w1")
	if err != nil {
		t.Fatal(err)
	}
	_ = m.MarkRemoteConnected(gen)
	m.SetRemoteBusy(true)
	if _, _, err := m.BeginRemoteConnect("b", "/w2"); err == nil {
		t.Fatal("expected busy rejection")
	}
	if err := m.DetachRemote(); err == nil {
		t.Fatal("expected busy detach rejection")
	}
	m.SetRemoteBusy(false)
	if err := m.DetachRemote(); err != nil {
		t.Fatal(err)
	}
	id, _, _ := m.Active()
	if id.Kind != KindLocal {
		t.Fatalf("after detach active = %+v", id)
	}
}
