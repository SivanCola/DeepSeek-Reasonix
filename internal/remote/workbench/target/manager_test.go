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
	// Returning to the same connected adapter is allowed even if it became busy
	// in the background; this does not replace the Host or transport.
	m.SetRemoteBusy(true)
	if active, _, _, err := m.ActivateRemote(rgen); err != nil || active.Kind != KindRemote {
		t.Fatalf("reactivate connected remote = %+v err=%v", active, err)
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

func TestManagerSeparatesAttachAndProjectionGenerations(t *testing.T) {
	m := New()
	_, attachGen, err := m.BeginRemoteConnect("lab", "/work")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkRemoteConnected(attachGen); err != nil {
		t.Fatal(err)
	}
	_, projectionGen, _, err := m.ActivateRemote(attachGen)
	if err != nil {
		t.Fatal(err)
	}
	if projectionGen == attachGen {
		t.Fatal("projection generation must fence the preceding connect transition")
	}
	remote := m.Remote()
	if remote == nil || remote.Generation != attachGen || !remote.Connected {
		t.Fatalf("remote lifecycle = %+v, want attach generation %d", remote, attachGen)
	}
	if !m.AbortRemoteConnect(attachGen) || m.Remote() != nil {
		t.Fatal("current failed generation was not cleared")
	}
	if m.AbortRemoteConnect(attachGen) {
		t.Fatal("stale abort unexpectedly cleared a newer state")
	}
}

func TestManagerUnexpectedDisconnectReturnsToLocalAndClearsBusy(t *testing.T) {
	m := New()
	_, attachGen, err := m.BeginRemoteConnect("lab", "/work")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkRemoteConnected(attachGen); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.ActivateRemote(attachGen); err != nil {
		t.Fatal(err)
	}
	m.SetRemoteBusy(true)
	id, _, _, changed := m.MarkRemoteDisconnected(attachGen)
	if !changed || id.Kind != KindLocal {
		t.Fatalf("disconnect = %+v changed=%v", id, changed)
	}
	remote := m.Remote()
	if remote == nil || remote.Connected {
		t.Fatalf("remote lifecycle = %+v", remote)
	}
	if _, _, err := m.BeginRemoteConnect("lab", "/work"); err != nil {
		t.Fatalf("reconnect remained fenced as busy: %v", err)
	}
	if _, _, _, changed := m.MarkRemoteDisconnected(attachGen); changed {
		t.Fatal("stale disconnect changed replacement connection")
	}
}
