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

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestHostKeyPolicyPromptsForNewKeyAlgorithm(t *testing.T) {
	hostname := "example.test:2222"
	knownED25519 := generateED25519Signer(t)
	presentedECDSA := generateECDSASigner(t)
	systemPath := filepath.Join(t.TempDir(), "known_hosts")
	managedPath := filepath.Join(t.TempDir(), "known_hosts")
	writeKnownHost(t, systemPath, hostname, knownED25519.PublicKey())

	prompted := 0
	policy := &HostKeyPolicy{
		SystemKnownHosts: []string{systemPath},
		ManagedPath:      managedPath,
		Prompt: func(_ context.Context, q HostKeyQuestion) (bool, error) {
			prompted++
			if q.KeyType != presentedECDSA.PublicKey().Type() {
				t.Fatalf("prompt key type = %q, want %q", q.KeyType, presentedECDSA.PublicKey().Type())
			}
			return true, nil
		},
	}

	callback, err := policy.Callback(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	remoteAddr := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 2222}
	if err := callback(hostname, remoteAddr, presentedECDSA.PublicKey()); err != nil {
		t.Fatalf("new key algorithm: %v", err)
	}
	if prompted != 1 {
		t.Fatalf("prompt count = %d, want 1", prompted)
	}

	// The accepted algorithm is persisted, so a fresh callback trusts the same
	// key without prompting again.
	callback, err = policy.Callback(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if err := callback(hostname, remoteAddr, presentedECDSA.PublicKey()); err != nil {
		t.Fatalf("persisted key algorithm: %v", err)
	}
	if prompted != 1 {
		t.Fatalf("persisted key re-prompted; count = %d", prompted)
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
