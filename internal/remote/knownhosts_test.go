package remote

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"reasonix/internal/remote/sshtest"
)

func TestNewSSHClientPrefersRecordedHostKeyAlgorithm(t *testing.T) {
	knownED25519 := generateED25519Signer(t)
	otherECDSA := generateECDSASigner(t)
	server := sshtest.Start(t, sshtest.Options{
		HostKeys: []ssh.Signer{otherECDSA, knownED25519},
	})
	systemPath := filepath.Join(t.TempDir(), "known_hosts")
	managedPath := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHost(t, systemPath, server.Addr, knownED25519.PublicKey())

	policy := &HostKeyPolicy{
		SystemKnownHosts: []string{systemPath},
		ManagedPath:      managedPath,
		Prompt: func(context.Context, HostKeyQuestion) (bool, error) {
			t.Fatal("known multi-algorithm host must not prompt")
			return false, nil
		},
	}

	_, hostName, port, err := ParseTarget(server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", server.Addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client, err := newSSHClient(context.Background(), conn, ResolvedHost{
		Name: server.Addr, HostName: hostName, Port: port, User: "test",
	}, &AuthOptions{DisableAgent: true}, policy, time.Second)
	if err != nil {
		t.Fatalf("connect to multi-algorithm host: %v", err)
	}
	defer client.Close()
}

func TestHostKeyPolicyRejectsChangedKeyAcrossAlgorithms(t *testing.T) {
	hostname := "example.test:2222"
	knownED25519 := generateED25519Signer(t)
	presentedECDSA := generateECDSASigner(t)
	systemPath := filepath.Join(t.TempDir(), "known_hosts")
	managedPath := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHost(t, systemPath, hostname, knownED25519.PublicKey())

	prompted := false
	policy := &HostKeyPolicy{
		SystemKnownHosts: []string{systemPath},
		ManagedPath:      managedPath,
		Prompt: func(context.Context, HostKeyQuestion) (bool, error) {
			prompted = true
			return true, nil
		},
	}
	callback, err := policy.Callback(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	err = callback(hostname, &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 2222}, presentedECDSA.PublicKey())
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("error = %v, want ErrHostKeyMismatch", err)
	}
	if prompted {
		t.Fatal("cross-algorithm mismatch must not be promptable")
	}
}

func TestHostKeyPolicyRejectsChangedKeyOfSameAlgorithm(t *testing.T) {
	hostname := "example.test:2222"
	knownKey := generateED25519Signer(t)
	presentedKey := generateED25519Signer(t)
	systemPath := filepath.Join(t.TempDir(), "known_hosts")
	managedPath := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHost(t, systemPath, hostname, knownKey.PublicKey())

	prompted := false
	policy := &HostKeyPolicy{
		SystemKnownHosts: []string{systemPath},
		ManagedPath:      managedPath,
		Prompt: func(context.Context, HostKeyQuestion) (bool, error) {
			prompted = true
			return true, nil
		},
	}
	callback, err := policy.Callback(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	err = callback(hostname, &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 2222}, presentedKey.PublicKey())
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("error = %v, want ErrHostKeyMismatch", err)
	}
	if prompted {
		t.Fatal("same-algorithm mismatch must not be promptable")
	}
}

func writeKnownHost(t *testing.T, path, hostname string, key ssh.PublicKey) {
	t.Helper()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func generateED25519Signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func generateECDSASigner(t *testing.T) ssh.Signer {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
