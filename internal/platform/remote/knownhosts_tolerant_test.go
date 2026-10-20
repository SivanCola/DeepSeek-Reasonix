package remote

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"reasonix/internal/base/testenv"
)

const wslZone = "vEthernet (WSL (Hyper-V firewall))"

func wslAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("fe80::215:5dff:fee8:5bc1"), Port: 22, Zone: wslZone}
}

type unrecordableAddr string

func (unrecordableAddr) Network() string  { return "tcp" }
func (a unrecordableAddr) String() string { return string(a) }

func loopback() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22} }

func keyText(key ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func managedPolicy(t *testing.T, accept bool, asked *int) (*HostKeyPolicy, string) {
	t.Helper()
	managed := filepath.Join(testenv.TempDir(t), "known_hosts")
	return &HostKeyPolicy{
		SystemKnownHosts: []string{filepath.Join(testenv.TempDir(t), "missing")},
		ManagedPath:      managed,
		Prompt: func(context.Context, HostKeyQuestion) (bool, error) {
			if asked != nil {
				*asked++
			}
			return accept, nil
		},
	}, managed
}

func TestAcceptedZoneAddressIsRecordedWithoutItsZone(t *testing.T) {
	key := generateECDSASigner(t).PublicKey()
	policy, managed := managedPolicy(t, true, nil)
	check, err := policy.Callback(context.Background(), "wsl")
	if err != nil {
		t.Fatal(err)
	}
	if err := check("pjc-wsl.local:22", wslAddr(), key); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(managed)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if n := len(strings.Fields(line)); n != 3 {
			t.Fatalf("record %q has %d fields, want host list, type and key", line, n)
		}
		if strings.ContainsAny(line, "%()") {
			t.Fatalf("record %q carries a zone", line)
		}
	}
	if _, err := knownhosts.New(managed); err != nil {
		t.Fatalf("the file written cannot be read back: %v", err)
	}
	strict := &HostKeyPolicy{SystemKnownHosts: []string{filepath.Join(testenv.TempDir(t), "missing")}, ManagedPath: managed}
	again, err := strict.Callback(context.Background(), "wsl")
	if err != nil {
		t.Fatal(err)
	}
	if err := again("pjc-wsl.local:22", wslAddr(), key); err != nil {
		t.Fatalf("the recorded host is not matched again: %v", err)
	}
}

func TestAnAddressKnownHostsCannotCarryIsNotRecorded(t *testing.T) {
	key := generateECDSASigner(t).PublicKey()
	for _, host := range []string{"bad host:22", "a,b:22", "wild*:22", "tab\thost:22", "new\nline:22", "pipe|host:22", "@marker:22"} {
		t.Run(host, func(t *testing.T) {
			policy, managed := managedPolicy(t, true, nil)
			check, err := policy.Callback(context.Background(), "x")
			if err != nil {
				t.Fatal(err)
			}
			if err := check(host, unrecordableAddr(host), key); err != nil {
				t.Fatalf("an accepted key for this session was refused: %v", err)
			}
			if data, _ := os.ReadFile(managed); len(bytes.TrimSpace(data)) != 0 {
				t.Fatalf("recorded %q", data)
			}
		})
	}
}

func brokenWSLLine(key ssh.PublicKey) string {
	return "pjc-wsl.local,fe80::215:5dff:fee8:5bc1%" + wslZone + " " + keyText(key)
}

type recordedLogs struct{ bytes.Buffer }

func captureWarnings(t *testing.T) *recordedLogs {
	t.Helper()
	logs := &recordedLogs{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs.Buffer, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func TestUnreadableManagedLinesAreSkippedWithAWarningAndTrustNothing(t *testing.T) {
	known := generateECDSASigner(t).PublicKey()
	broken := generateECDSASigner(t).PublicKey()
	asked := 0
	policy, managed := managedPolicy(t, true, &asked)
	good := knownhosts.Line([]string{knownhosts.Normalize("good.example:22")}, known)
	content := brokenWSLLine(broken) + "\n" + brokenWSLLine(broken) + "\n" + good + "\n"
	if err := os.WriteFile(managed, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := captureWarnings(t)

	check, err := policy.Callback(context.Background(), "good")
	if err != nil {
		t.Fatalf("one unreadable line made the file unusable: %v", err)
	}
	if err := check("good.example:22", loopback(), known); err != nil || asked != 0 {
		t.Fatalf("a readable record next to the broken ones was not honoured: err=%v asked=%d", err, asked)
	}
	if err := check("pjc-wsl.local:22", loopback(), broken); err != nil || asked != 1 {
		t.Fatalf("a host named only by a skipped line must be asked about again: err=%v asked=%d", err, asked)
	}
	out := logs.String()
	for _, want := range []string{managed, "line=1", "line=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning %q does not name %q", out, want)
		}
	}
	if strings.Contains(out, keyText(broken)) || strings.Contains(out, "AAAA") {
		t.Errorf("warning carries key material: %q", out)
	}
	if after, _ := os.ReadFile(managed); !strings.HasPrefix(string(after), brokenWSLLine(broken)) {
		t.Error("the user's file was rewritten")
	}
	if left, _ := filepath.Glob(managed + ".*"); len(left) != 0 {
		t.Errorf("working copies left behind: %v", left)
	}
}

func TestASkippedLineGrantsNoTrustInStrictMode(t *testing.T) {
	broken := generateECDSASigner(t).PublicKey()
	managed := filepath.Join(testenv.TempDir(t), "known_hosts")
	if err := os.WriteFile(managed, []byte(brokenWSLLine(broken)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := &HostKeyPolicy{SystemKnownHosts: []string{filepath.Join(testenv.TempDir(t), "missing")}, ManagedPath: managed}
	check, err := policy.Callback(context.Background(), "wsl")
	if err != nil {
		t.Fatal(err)
	}
	if err := check("pjc-wsl.local:22", loopback(), broken); !errors.Is(err, ErrHostKeyRejected) {
		t.Fatalf("a key named only by a broken line was accepted: %v", err)
	}
}

func TestAMismatchAfterSkippedLinesNamesTheUsersFileAndLine(t *testing.T) {
	recorded, presented := generateECDSASigner(t).PublicKey(), generateECDSASigner(t).PublicKey()
	managed := filepath.Join(testenv.TempDir(t), "known_hosts")
	content := brokenWSLLine(recorded) + "\n" + "\n" + knownhosts.Line([]string{knownhosts.Normalize("good.example:22")}, recorded) + "\n"
	if err := os.WriteFile(managed, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := &HostKeyPolicy{SystemKnownHosts: []string{filepath.Join(testenv.TempDir(t), "missing")}, ManagedPath: managed}
	check, err := policy.Callback(context.Background(), "good")
	if err != nil {
		t.Fatal(err)
	}
	var mismatch *HostKeyMismatchError
	if err := check("good.example:22", loopback(), presented); !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want a host key mismatch", err)
	}
	if got := mismatch.Locations; len(got) != 1 || got[0].Filename != managed || got[0].Line != 3 {
		t.Fatalf("mismatch locations = %+v, want %s:3", got, managed)
	}
}

func TestUnreadableLinesStillFailClosedWhereSkippingCouldWidenTrust(t *testing.T) {
	key := generateECDSASigner(t).PublicKey()
	missing := filepath.Join(testenv.TempDir(t), "missing")
	for name, tc := range map[string]struct{ system, managed string }{
		"a corrupt marker line in the managed file": {managed: "@revoked pjc-wsl.local " + "ecdsa-sha2-nistp256 !!!\n"},
		"a corrupt line in a system file":           {system: brokenWSLLine(key) + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := testenv.TempDir(t)
			managed := filepath.Join(dir, "known_hosts")
			system := missing
			if tc.system != "" {
				system = filepath.Join(dir, "system_known_hosts")
				if err := os.WriteFile(system, []byte(tc.system), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(managed, []byte(tc.managed), 0o600); err != nil {
				t.Fatal(err)
			}
			policy := &HostKeyPolicy{SystemKnownHosts: []string{system}, ManagedPath: managed}
			if _, err := policy.Callback(context.Background(), "x"); err == nil {
				t.Fatal("the load went through with a line it could not read")
			}
		})
	}
}

func TestARecordIsNotJoinedOntoAnUnterminatedLastLine(t *testing.T) {
	first, second := generateECDSASigner(t).PublicKey(), generateECDSASigner(t).PublicKey()
	policy, managed := managedPolicy(t, true, nil)
	line := knownhosts.Line([]string{knownhosts.Normalize("one.example:22")}, first)
	if err := os.WriteFile(managed, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	check, err := policy.Callback(context.Background(), "two")
	if err != nil {
		t.Fatal(err)
	}
	if err := check("two.example:22", loopback(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := knownhosts.New(managed); err != nil {
		t.Fatalf("the second record was joined onto the first: %v", err)
	}
}
