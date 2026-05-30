package cli

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// SSHConfig holds a parsed ssh:// URI.
type SSHConfig struct {
	User       string
	Host       string
	Port       string
	RemotePath string
}

// parseSSHURI parses an ssh://[user@]host[:port][/path] URI.
func parseSSHURI(raw string) (*SSHConfig, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid SSH URI: %w", err)
	}
	if u.Scheme != "ssh" {
		return nil, fmt.Errorf("expected ssh:// URI, got %q", raw)
	}
	cfg := &SSHConfig{
		Host:       u.Hostname(),
		Port:       u.Port(),
		RemotePath: strings.TrimPrefix(u.Path, "/"),
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("SSH URI %q has no host", raw)
	}
	if cfg.Port == "" {
		cfg.Port = "22"
	}
	if u.User != nil {
		cfg.User = u.User.Username()
	}
	return cfg, nil
}

// probeSSH checks whether the SSH host is reachable with the given config.
func probeSSH(cfg *SSHConfig) error {
	target := cfg.Host
	if cfg.User != "" {
		target = cfg.User + "@" + target
	}
	cmd := exec.Command("ssh",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", cfg.Port,
		target,
		"echo ok",
	)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("SSH probe failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// generateSSHDryRunReport builds a human-readable report for an SSH workspace.
func generateSSHDryRunReport(cfg *SSHConfig, probeErr error) string {
	var b strings.Builder
	b.WriteString("=== SSH Remote Workspace RFC (dry-run) ===\n\n")
	if cfg.User != "" {
		b.WriteString(fmt.Sprintf("  User:       %s\n", cfg.User))
	}
	b.WriteString(fmt.Sprintf("  Host:       %s\n", cfg.Host))
	b.WriteString(fmt.Sprintf("  Port:       %s\n", cfg.Port))
	if cfg.RemotePath != "" {
		b.WriteString(fmt.Sprintf("  RemotePath:  /%s\n", cfg.RemotePath))
	}
	b.WriteString("\n")
	if probeErr != nil {
		b.WriteString(fmt.Sprintf("SSH probe: FAILED — %v\n\n", probeErr))
	} else {
		b.WriteString("SSH probe: OK\n\n")
	}
	b.WriteString("Planned steps:\n")
	b.WriteString("  1. Validate local SSH client availability\n")
	b.WriteString("  2. Connect to remote host via SSH\n")
	b.WriteString("  3. Verify remote project path exists\n")
	b.WriteString("  4. Mount remote workspace (requires sshfs or similar)\n")
	b.WriteString("\nRecommendation: Run Reasonix directly on the remote host\n")
	b.WriteString("and access the dashboard via SSH tunnel:\n")
	b.WriteString("  ssh -L 8420:localhost:8420 user@host\n")
	b.WriteString("  # then on remote: reasonix serve\n")
	return b.String()
}
