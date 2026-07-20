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

type closeTrackingStream struct{ closed bool }

func (*closeTrackingStream) Read([]byte) (int, error)    { return 0, io.EOF }
func (*closeTrackingStream) Write(p []byte) (int, error) { return len(p), nil }
func (s *closeTrackingStream) Close() error              { s.closed = true; return nil }

var _ transport.Stream = (*closeTrackingStream)(nil)
