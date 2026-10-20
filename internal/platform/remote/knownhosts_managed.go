package remote

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/crypto/ssh/knownhosts"
)

// recordableHost is the host field known_hosts may carry for an address: OpenSSH
// never writes an IPv6 zone, and a field with whitespace, a control character or a
// pattern or list delimiter would be read back as something else than the address.
func recordableHost(address string) (string, bool) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host, port = address, ""
	}
	if zone := strings.IndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	if host == "" || strings.ContainsFunc(host, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`,|*?!@"'#\[]`, r)
	}) {
		return "", false
	}
	if port == "" {
		return knownhosts.Normalize(host), true
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return knownhosts.Normalize(net.JoinHostPort(host, port)), true
}

// recordableHosts keeps the addresses known_hosts can carry, once each.
func recordableHosts(addresses ...string) []string {
	var out []string
	for _, address := range addresses {
		host, ok := recordableHost(address)
		if !ok {
			continue
		}
		seen := false
		for _, have := range out {
			seen = seen || have == host
		}
		if !seen {
			out = append(out, host)
		}
	}
	return out
}

// tolerantManagedFiles answers a failed load of the managed file with the
// unreadable lines blanked out in place, so every line keeps its number, and the
// numbers it skipped. A skipped line trusts nothing; its host is asked about again.
// A marker line (@revoked, @cert-authority) is never skipped: losing one could turn
// a refusal into an acceptance, so an unreadable one keeps the load failed.
func tolerantManagedFiles(files []string, managed string) (repaired []string, skipped []int, cleanup func(), ok bool) {
	data, err := os.ReadFile(managed)
	if err != nil {
		return nil, nil, nil, false
	}
	dir := filepath.Dir(managed)
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		text := bytes.TrimSpace(line)
		if len(text) == 0 || text[0] == '#' || readsAsKnownHosts(dir, text) {
			continue
		}
		if text[0] == '@' {
			return nil, nil, nil, false
		}
		skipped = append(skipped, i+1)
		lines[i] = nil
	}
	if len(skipped) == 0 {
		return nil, nil, nil, false
	}
	f, err := os.CreateTemp(dir, filepath.Base(managed)+".repaired-*")
	if err != nil {
		return nil, nil, nil, false
	}
	cleanup = func() { _ = os.Remove(f.Name()) }
	if _, err := f.Write(bytes.Join(lines, []byte("\n"))); err != nil {
		_ = f.Close()
		cleanup()
		return nil, nil, nil, false
	}
	if err := f.Close(); err != nil {
		cleanup()
		return nil, nil, nil, false
	}
	for _, file := range files {
		if file == managed {
			file = f.Name()
		}
		repaired = append(repaired, file)
	}
	return repaired, skipped, cleanup, true
}

// readsAsKnownHosts asks the parser that will read the file whether it accepts
// one line, rather than a second parser's idea of a valid one.
func readsAsKnownHosts(dir string, line []byte) bool {
	f, err := os.CreateTemp(dir, "known_hosts.line-*")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(append(append([]byte(nil), line...), '\n'))
	if cerr := f.Close(); werr != nil || cerr != nil {
		return false
	}
	_, err = knownhosts.New(f.Name())
	return err == nil
}

// remapKnownHostsFile points a lookup answer at the file the user has rather than
// the repaired copy the lookup ran against; line numbers are the same by construction.
func remapKnownHostsFile(err error, from, to string) error {
	var ke *knownhosts.KeyError
	if errors.As(err, &ke) {
		for i := range ke.Want {
			if ke.Want[i].Filename == from {
				ke.Want[i].Filename = to
			}
		}
	}
	return err
}

func warnSkippedKnownHostsLines(managed string, skipped []int) {
	for _, line := range skipped {
		slog.Warn("remote: ignoring an unreadable known_hosts line; its host will be asked about again",
			"file", managed, "line", line)
	}
}

// endsInNewline reports that the file is empty or its last byte is a newline, so
// a record appended to it starts a line of its own.
func endsInNewline(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return true, err
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return false, fmt.Errorf("read known_hosts tail: %w", err)
	}
	return last[0] == '\n', nil
}
