package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"reasonix/internal/provider"
	"reasonix/internal/remote/broker"
	"reasonix/internal/remote/remoteruntime"
)

// runRemoteRuntime starts the headless multi-session remote workspace server.
// It is launched on the SSH host by desktop bootstrap. State files live under a
// separate directory from `reasonix serve` so PID/port/token never collide.
func runRemoteRuntime(args []string, version string) int {
	fs := flag.NewFlagSet("remote-runtime", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "listen address (loopback only)")
	workspace := fs.String("workspace", "", "remote workspace root (required)")
	tokenFile := fs.String("token-file", "", "path to auth token file (mode 0600)")
	portFile := fs.String("port-file", "", "write bound host:port after listen")
	pidFile := fs.String("pid-file", "", "write process id")
	brokerURL := fs.String("broker-url", "", "provider broker base URL via reverse tunnel")
	brokerTokenFile := fs.String("broker-token-file", "", "capability token file for the provider broker")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ws := strings.TrimSpace(*workspace)
	if ws == "" {
		if cwd, err := os.Getwd(); err == nil {
			ws = cwd
		}
	}
	if ws == "" {
		fmt.Fprintln(os.Stderr, "remote-runtime: --workspace is required")
		return 2
	}
	if abs, err := filepath.Abs(ws); err == nil {
		ws = abs
	}

	token := ""
	if *tokenFile != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "remote-runtime: read token-file:", err)
			return 1
		}
		token = strings.TrimSpace(string(data))
	}

	var resolver provider.Resolver
	if strings.TrimSpace(*brokerURL) != "" {
		tok, err := readBrokerToken(*brokerTokenFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "remote-runtime:", err)
			return 1
		}
		resolver = &broker.Client{BaseURL: strings.TrimSpace(*brokerURL), Token: tok}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := remoteruntime.New(remoteruntime.Options{
		Workspace: ws,
		Version:   version,
		Token:     token,
		Resolver:  resolver,
	})
	defer srv.Close()

	bound, err := srv.ListenAndServe(ctx, *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "remote-runtime: listen:", err)
		return 1
	}
	if *portFile != "" {
		if err := remoteruntime.WritePortFile(*portFile, bound.String()); err != nil {
			fmt.Fprintln(os.Stderr, "remote-runtime: port-file:", err)
			return 1
		}
	}
	if *pidFile != "" {
		if err := os.WriteFile(*pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "remote-runtime: pid-file:", err)
			return 1
		}
	}
	// Never print tokens. Bound address is useful for supervisors.
	fmt.Fprintf(os.Stderr, "remote-runtime listening on %s workspace=%s\n", bound.String(), ws)
	<-ctx.Done()
	return 0
}

func readBrokerToken(path string) (broker.CapabilityToken, error) {
	if strings.TrimSpace(path) == "" {
		return broker.CapabilityToken{}, fmt.Errorf("broker-token-file is required when --broker-url is set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return broker.CapabilityToken{}, fmt.Errorf("read broker-token-file: %w", err)
	}
	if info, err := os.Stat(path); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			return broker.CapabilityToken{}, fmt.Errorf("broker-token-file permissions are too broad")
		}
	}
	return broker.ParseCapabilityToken(strings.TrimSpace(string(data)))
}
