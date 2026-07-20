package main

import (
	"context"
	"io"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/remote/workbench/transport"
)

func TestMapConfigHostKeepsEverySSHConfigAliasInConfigMode(t *testing.T) {
	for _, entry := range []config.RemoteHostEntry{
		{Name: "dotted", Host: "gpu.corp.example", UseSSHConfig: true},
		{Name: "explicit-user", Host: "gpu-alias", User: "builder", UseSSHConfig: true},
	} {
		got, err := mapConfigHostToWorkbenchEntry(entry)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mode != RemoteHostConnectionConfig || got.Alias != entry.Host {
			t.Fatalf("entry %+v mapped to %+v", entry, got)
		}
	}
}

func TestAskPassOwnedStreamClosesBrokerWithTransport(t *testing.T) {
	broker, err := StartRemoteAskPassBroker(context.Background(), time.Minute, func(context.Context, RemoteAskPassPrompt) (RemoteAskPassAnswer, error) {
		return RemoteAskPassAnswer{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := &closeTrackingStream{}
	owned := &askPassOwnedStream{Stream: stream, broker: broker}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if !stream.closed {
		t.Fatal("transport was not closed")
	}
	if _, err := broker.SSHEnvironment("/absolute/helper"); err == nil {
		t.Fatal("AskPass capability remained live after transport close")
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeWorkbenchPeerFailsClosed(t *testing.T) {
	factory := &peerIdentityFactory{}
	if err := authorizeWorkbenchPeer(factory, "SHA256:trusted"); err == nil {
		t.Fatal("missing live peer identity was authorized")
	}
	factory.peer = workbenchPeerIdentity{KeyType: "ssh-ed25519", Fingerprint: "SHA256:other"}
	if err := authorizeWorkbenchPeer(factory, "SHA256:trusted"); err == nil {
		t.Fatal("changed peer identity was authorized")
	}
	factory.peer.Fingerprint = "SHA256:trusted"
	if err := authorizeWorkbenchPeer(factory, "SHA256:trusted"); err != nil {
		t.Fatalf("trusted live peer was rejected: %v", err)
	}
	if err := authorizeWorkbenchPeer(transport.FactoryFunc(func(context.Context) (transport.Stream, error) {
		return nil, nil
	}), "SHA256:trusted"); err == nil {
		t.Fatal("transport without peer identity reporting was authorized")
	}
}

type peerIdentityFactory struct {
	peer workbenchPeerIdentity
}

func (*peerIdentityFactory) Open(context.Context) (transport.Stream, error) { return nil, nil }

func (f *peerIdentityFactory) PeerIdentity() (workbenchPeerIdentity, bool) {
	return f.peer, f.peer.Fingerprint != ""
}

type closeTrackingStream struct{ closed bool }

func (*closeTrackingStream) Read([]byte) (int, error)    { return 0, io.EOF }
func (*closeTrackingStream) Write(p []byte) (int, error) { return len(p), nil }
func (s *closeTrackingStream) Close() error              { s.closed = true; return nil }

var _ transport.Stream = (*closeTrackingStream)(nil)
