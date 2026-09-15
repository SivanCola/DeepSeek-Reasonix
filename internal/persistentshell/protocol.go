package persistentshell

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	readyToken          = "REASONIX_SHELL_READY"
	maxPersistentOutput = 10 << 20
)

func newMarkerID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", b[:])
	}
	return hex.EncodeToString(b[:])
}

func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func posixSetupScript() string {
	return strings.Join([]string{
		"stty -echo 2>/dev/null || true",
		"unset PROMPT_COMMAND",
		"PS1=",
		"PS2=",
		"PS4=",
		"set +H 2>/dev/null || true",
		"printf '%s\\n' " + posixQuote(readyToken),
	}, "; ") + "\n"
}

func posixCommandScript(command, start, end string) string {
	return "printf '%s\\n' " + posixQuote(start) +
		"; eval " + posixQuote(command) +
		"\nprintf '%s%d\\n' " + posixQuote(end) + " $?\n"
}

func powerShellSetupScript() string {
	return "$OutputEncoding=[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; " +
		"Write-Output " + powershellSingleQuote(readyToken) + "\n"
}

func powerShellCommandScript(command, start, end string) string {
	encoded := hex.EncodeToString([]byte(command))
	return strings.Join([]string{
		"$__rx_start=" + powershellSingleQuote(start),
		"$__rx_end=" + powershellSingleQuote(end),
		"Write-Output $__rx_start",
		"$__rx_hex=" + powershellSingleQuote(encoded),
		"$__rx_cmd=-join (for ($i=0; $i -lt $__rx_hex.Length; $i+=2) { [char][Convert]::ToInt32($__rx_hex.Substring($i,2),16) })",
		"$global:LASTEXITCODE=0",
		"Invoke-Expression $__rx_cmd",
		"if ($null -eq $global:LASTEXITCODE) { $__rx_code = $(if ($?) { 0 } else { 1 }) } else { $__rx_code = [int]$global:LASTEXITCODE }",
		"Write-Output ($__rx_end + $__rx_code)",
	}, "; ") + "\n"
}

func powershellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func normalizePTY(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func hasReady(buf string) bool {
	return linePresent(normalizePTY(buf), readyToken)
}

func extractOutput(buf, start, end string) (body string, code int, ok bool) {
	text := normalizePTY(buf)
	startIdx := indexLine(text, start)
	if startIdx < 0 {
		return "", 0, false
	}
	after := text[startIdx+len(start):]
	after = strings.TrimPrefix(after, "\n")
	endIdx := indexLinePrefix(after, end)
	if endIdx < 0 {
		return "", 0, false
	}
	body = after[:endIdx]
	if strings.HasSuffix(body, "\n") {
		body = strings.TrimSuffix(body, "\n")
	}
	rest := after[endIdx:]
	line, _, _ := strings.Cut(rest, "\n")
	digits := strings.TrimPrefix(line, end)
	n, err := strconv.Atoi(digits)
	if err != nil {
		return "", 0, false
	}
	return body, n, true
}

func linePresent(text, line string) bool {
	return indexLine(text, line) >= 0
}

func indexLine(text, line string) int {
	if text == line {
		return 0
	}
	if strings.HasPrefix(text, line+"\n") {
		return 0
	}
	needle := "\n" + line
	if i := strings.Index(text, needle+"\n"); i >= 0 {
		return i + 1
	}
	if strings.HasSuffix(text, needle) {
		return len(text) - len(line)
	}
	return -1
}

func indexLinePrefix(text, prefix string) int {
	if strings.HasPrefix(text, prefix) {
		return 0
	}
	needle := "\n" + prefix
	if i := strings.Index(text, needle); i >= 0 {
		return i + 1
	}
	return -1
}

func boundOutput(s string) string {
	if len(s) <= maxPersistentOutput {
		return s
	}
	keep := maxPersistentOutput / 8
	if keep > 64<<10 {
		keep = 64 << 10
	}
	head := s[:maxPersistentOutput-keep]
	for !utf8.RuneStart(head[len(head)-1]) && len(head) > 0 {
		head = head[:len(head)-1]
	}
	tail := s[len(s)-keep:]
	for !utf8.RuneStart(tail[0]) && len(tail) > 1 {
		tail = tail[1:]
	}
	return head + "\n\n...[shell output truncated at 10 MiB; showing the final 64 KiB]...\n\n" + tail
}
