package remote

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"reasonix/internal/remote/forward"
	"reasonix/internal/remote/sshtest"
)

// TestSSHLocalAndReverseForwardsE2E exercises the SSH port-forward shapes used
// by remote desktop: -L to remote-runtime and -R to the local Provider Broker.
func TestSSHLocalAndReverseForwardsE2E(t *testing.T) {
	// Local "broker" service.
	brokerLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer brokerLn.Close()
	brokerHit := make(chan struct{}, 1)
	go func() {
		for {
			c, err := brokerLn.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
				select {
				case brokerHit <- struct{}{}:
				default:
				}
			}(c)
		}
	}()

	// Remote-side "runtime" service (what -L will target).
	runtimeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeLn.Close()
	runtimePort := runtimeLn.Addr().(*net.TCPAddr).Port
	go func() {
		_ = http.Serve(runtimeLn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
	}()

	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	c := newTestClient(t, srv, Options{
		Auth: AuthOptions{
			DisableAgent: true,
			Password:     func() (string, error) { return "hunter2", nil },
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	set := c.Forwards()

	// -R: remote 127.0.0.1:0 → local broker
	remoteBound, err := set.Add(forward.Spec{
		Name:       "provider-broker",
		Direction:  forward.Remote,
		BindAddr:   "127.0.0.1:0",
		TargetAddr: brokerLn.Addr().String(),
	})
	if err != nil {
		t.Fatalf("reverse forward: %v", err)
	}
	if remoteBound == "" {
		t.Fatal("empty reverse bound addr")
	}

	// -L: local 127.0.0.1:0 → remote runtime
	localBound, err := set.Add(forward.Spec{
		Name:       "remote-runtime",
		Direction:  forward.Local,
		BindAddr:   "127.0.0.1:0",
		TargetAddr: fmt.Sprintf("127.0.0.1:%d", runtimePort),
	})
	if err != nil {
		t.Fatalf("local forward: %v", err)
	}

	// Hit remote-runtime via local forward.
	resp, err := http.Get("http://" + localBound + "/")
	if err != nil {
		t.Fatalf("local forward GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "ok") {
		t.Fatalf("runtime via -L: status=%d body=%s", resp.StatusCode, body)
	}

	// Hit local broker via reverse-bound address on this host.
	conn, err := net.DialTimeout("tcp", remoteBound, 3*time.Second)
	if err != nil {
		t.Fatalf("dial reverse bound %s: %v", remoteBound, err)
	}
	_, _ = io.WriteString(conn, "GET / HTTP/1.0\r\n\r\n")
	_ = conn.Close()
	select {
	case <-brokerHit:
	case <-time.After(3 * time.Second):
		t.Fatal("broker was not reached via -R")
	}
}
