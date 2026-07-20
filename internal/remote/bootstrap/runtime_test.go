package bootstrap

import (
	"strings"
	"testing"
)

func TestLaunchRemoteRuntimeCommandQuotesAndBrokerFlags(t *testing.T) {
	p := StatePaths{
		Dir:       "/home/u/.reasonix/remote-runtime",
		TokenFile: "/home/u/.reasonix/remote-runtime/runtime-ws.token",
		PortFile:  "/home/u/.reasonix/remote-runtime/runtime-ws.port",
		PidFile:   "/home/u/.reasonix/remote-runtime/runtime-ws.pid",
		LogFile:   "/home/u/.reasonix/remote-runtime/runtime-ws.log",
	}
	cmd := LaunchRemoteRuntimeCommand(
		"/usr/bin/reasonix",
		"/home/u/proj with spaces",
		p,
		"http://127.0.0.1:9",
		"/home/u/.reasonix/remote-runtime/runtime-ws.broker-token",
	)
	for _, needle := range []string{
		"remote-runtime",
		"--workspace",
		"--token-file",
		"--port-file",
		"--pid-file",
		"--broker-url",
		"--broker-token-file",
		"http://127.0.0.1:9",
	} {
		if !strings.Contains(cmd, needle) {
			t.Fatalf("command missing %q: %s", needle, cmd)
		}
	}
	// Shell-quoted workspace must not leave raw space-breakable path.
	if strings.Contains(cmd, "cd /home/u/proj with spaces") {
		t.Fatalf("workspace not shell-quoted: %s", cmd)
	}
	if !strings.Contains(cmd, "proj with spaces") {
		t.Fatalf("workspace missing: %s", cmd)
	}
}

func TestRuntimePathsSeparateFromServe(t *testing.T) {
	home := "/home/u"
	ws := "/home/u/app"
	serve := pathsFor(home, ws)
	rt := runtimePathsFor(home, ws)
	if serve.Dir == rt.Dir {
		t.Fatalf("serve and runtime dirs must differ: %s", serve.Dir)
	}
	if !strings.Contains(rt.Dir, "remote-runtime") {
		t.Fatalf("runtime dir = %s", rt.Dir)
	}
	if strings.Contains(rt.StateJSON, "serve-") {
		t.Fatalf("runtime state must not use serve- prefix: %s", rt.StateJSON)
	}
	if !strings.Contains(rt.StateJSON, "runtime-") {
		t.Fatalf("runtime state = %s", rt.StateJSON)
	}
}

func TestLocateRemoteRuntimeCommand(t *testing.T) {
	cmd := LocateRemoteRuntimeCommand("/opt/reasonix")
	if !strings.Contains(cmd, "remote-runtime --help") {
		t.Fatalf("missing remote-runtime probe: %s", cmd)
	}
	if !strings.Contains(cmd, "runtime:yes") {
		t.Fatalf("missing runtime marker: %s", cmd)
	}
}

func TestRuntimeAliveAndStopCommands(t *testing.T) {
	p := StatePaths{TokenFile: "/t", PortFile: "/p"}
	alive := RuntimeAliveCommand(42, p)
	if !strings.Contains(alive, "remote-runtime") || !strings.Contains(alive, "42") {
		t.Fatalf("alive = %s", alive)
	}
	stop := StopRemoteRuntimeCommand(42, p)
	if !strings.Contains(stop, "kill -TERM 42") {
		t.Fatalf("stop = %s", stop)
	}
}
