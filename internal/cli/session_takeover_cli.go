package cli

// Local takeover for the CLI resume paths: when the session lease is held by a
// resident serve process on this machine (left behind by a remote desktop that
// connected over SSH), the user can take the session over instead of exiting
// with a refusal. Serve releases the lease via POST /handoff; the remote tab
// keeps watching read-only through the frame mirror.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/remote/bootstrap"
	"reasonix/internal/store"
)

// cliTakeoverTimeout bounds the drain window of a wait-mode takeover.
const cliTakeoverTimeout = 2 * time.Minute

type cliServeRecord struct {
	pid   int
	base  string
	token string
}

// discoverCLIServes enumerates resident serve processes recorded under
// <Reasonix home>/remote. This machine is the SSH target in the takeover
// scenario, so the bootstrap's SFTP-written state files are local files here.
func discoverCLIServes() []cliServeRecord {
	dir := config.RemoteStateDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []cliServeRecord
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "serve-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		state, err := bootstrap.UnmarshalState(data)
		if err != nil || state.PID <= 0 {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(name, "serve-"), ".json")
		addr := state.Addr
		if port, err := os.ReadFile(filepath.Join(dir, store.RemoteServePortName(slug))); err == nil {
			if trimmed := strings.TrimSpace(string(port)); trimmed != "" {
				addr = trimmed
			}
		}
		if addr == "" {
			continue
		}
		token := ""
		if data, err := os.ReadFile(filepath.Join(dir, store.RemoteServeTokenName(slug))); err == nil {
			token = strings.TrimSpace(string(data))
		}
		if token == "" {
			continue
		}
		out = append(out, cliServeRecord{pid: state.PID, base: "http://" + addr, token: token})
	}
	return out
}

// cliServeForPID finds the resident serve holding the lease by matching the
// holder PID the lease error reported.
func cliServeForPID(pid int) *cliServeRecord {
	records := discoverCLIServes()
	for i := range records {
		if records[i].pid == pid {
			return &records[i]
		}
	}
	return nil
}

func cliServeRequest(ctx context.Context, record cliServeRecord, method, path string, body []byte) (*http.Response, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar}
	auth, _ := json.Marshal(map[string]string{"token": record.token})
	authReq, err := http.NewRequestWithContext(ctx, http.MethodPost, record.base+"/auth/token", bytes.NewReader(auth))
	if err != nil {
		return nil, err
	}
	authReq.Header.Set("Content-Type", "application/json")
	authResp, err := client.Do(authReq)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, authResp.Body)
	authResp.Body.Close()
	if authResp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("serve auth: status %d", authResp.StatusCode)
	}
	req, err := http.NewRequestWithContext(ctx, method, record.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}

// cliTakeoverHeldSession releases the lease a resident serve on this machine
// holds on sessionPath. err is the original lease-acquisition failure; it is
// used to identify (and report) the holder. A nil return means the caller may
// retry acquiring the lease.
func cliTakeoverHeldSession(sessionPath string, leaseErr error) error {
	pid := 0
	var leaseError *agent.SessionLeaseError
	if errors.As(leaseErr, &leaseError) && leaseError != nil && leaseError.Info != nil {
		pid = leaseError.Info.PID
	}
	if pid <= 0 {
		return fmt.Errorf("%w; no local serve identity to take over from", agent.ErrSessionLeaseHeld)
	}
	record := cliServeForPID(pid)
	if record == nil {
		return fmt.Errorf("%w; holder pid %d is not a resident serve on this machine", agent.ErrSessionLeaseHeld, pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliTakeoverTimeout+15*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"sessionPath": sessionPath,
		"force":       true,
		"mode":        "wait",
		"timeoutMs":   cliTakeoverTimeout.Milliseconds(),
	})
	resp, err := cliServeRequest(ctx, *record, http.MethodPost, "/handoff", body)
	if err != nil {
		return fmt.Errorf("takeover from local serve (pid %d): %w", pid, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("takeover from local serve (pid %d): %s", pid, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// cliSessionTakeoverCandidate reports whether leaseErr points at a resident
// serve on this machine — the case where a takeover offer makes sense.
func cliSessionTakeoverCandidate(leaseErr error) bool {
	var leaseError *agent.SessionLeaseError
	if !errors.As(leaseErr, &leaseError) || leaseError == nil || leaseError.Info == nil {
		return false
	}
	return cliServeForPID(leaseError.Info.PID) != nil
}

// promptSessionTakeover asks on the terminal (pre-TUI startup) whether to take
// the held session over. Non-interactive sessions answer no.
func promptSessionTakeover(leaseErr error) bool {
	if !isInteractive() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s\n", sessionLeaseResumeRefusal(leaseErr))
	fmt.Fprint(os.Stderr, "take over the session from this machine's resident serve? [y/N] ")
	answer, err := readCLITakeoverAnswer()
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func readCLITakeoverAnswer() (string, error) {
	buf := make([]byte, 64)
	n, err := os.Stdin.Read(buf)
	if n > 0 {
		return string(buf[:n]), nil
	}
	return "", err
}
