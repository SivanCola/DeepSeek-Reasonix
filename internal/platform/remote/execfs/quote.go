package execfs

import "strings"

// ShellQuote wraps s in single quotes safe for POSIX sh, escaping an embedded
// single quote as '\”. It is the only quoting used for a word handed to a
// remote shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CommandLine is the one-line form of running body under /bin/sh with args as
// $1..., for a transport that only takes a command string. The script and each
// argument are quoted whole, so the account's shell sees data, not syntax.
func CommandLine(body string, args ...string) string {
	var b strings.Builder
	b.WriteString("/bin/sh -c ")
	b.WriteString(ShellQuote(body))
	b.WriteString(" sh")
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(ShellQuote(a))
	}
	return b.String()
}
