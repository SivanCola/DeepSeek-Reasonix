package permission

import "strings"

// readOnlyBashCommands is the set of commands considered read-only — they
// don't modify filesystem state, network state, or process state. Each
// entry is the first word of a bash command (lowercased). Commands not in
// this set that might also be read-only (e.g. "git log") are handled
// separately by isReadOnlyBashSubject.
var readOnlyBashCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "less": true, "more": true,
	"ls": true, "find": true, "locate": true, "which": true, "whereis": true, "type": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true,
	"echo": true, "printf": true,
	"pwd": true, "whoami": true, "id": true, "uname": true, "hostname": true,
	"date": true, "env": true, "printenv": true,
	"wc": true, "sort": true, "uniq": true, "cut": true, "tr": true,
	"stat": true, "file": true, "du": true, "df": true,
	"ps": true, "top": true, "htop": true,
	"diff": true, "cmp": true, "comm": true,
	"awk": true, "sed": true,
	"man": true, "info": true, "help": true,
	"true": true, "false": true, "test": true, "[": true,
	"basename": true, "dirname": true, "realpath": true, "readlink": true,
}

// readOnlyBashPrefixes are command prefixes where the second word
// determines read-only status. Each maps to the set of read-only
// subcommands.
var readOnlyBashPrefixes = map[string]map[string]bool{
	"git": {
		"log": true, "status": true, "diff": true, "show": true,
		"tag": true, "remote": true,
		"blame": true, "grep": true, "ls-files": true, "ls-tree": true,
		"rev-parse": true, "rev-list": true, "describe": true,
		"config": true, "stash": true, "reflog": true,
		"shortlog": true, "whatchanged": true, "cherry": true,
		"archive": true, "bundle": true,
		"cat-file": true, "for-each-ref": true, "name-rev": true,
	},
	"go": {
		"vet": true, "doc": true, "list": true,
		"version": true, "env": true,
	},
	"npm": {
		"ls": true, "list": true, "view": true, "info": true,
		"outdated": true, "audit": true,
	},
	"cargo": {
		"check": true, "doc": true, "search": true,
	},
	"docker": {
		"ps": true, "images": true, "inspect": true, "logs": true,
		"stats": true, "info": true, "version": true,
	},
	"kubectl": {
		"get": true, "describe": true, "logs": true, "explain": true,
		"api-resources": true, "api-versions": true,
	},
}

// isReadOnlyBashSubject returns true when a bash command is a known
// read-only operation. The subject is the JSON arg value extracted by
// Subject() — for bash it is the raw command string.
func isReadOnlyBashSubject(subject string) bool {
	cmd := strings.TrimSpace(subject)
	if cmd == "" {
		return false
	}
	if strings.ContainsAny(cmd, ";|&\n") {
		return false
	}
	// Split the first word of the command (before space, ;, |, &, or newline).
	first := strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ' ' || r == '\t' || r == ';' || r == '|' || r == '&' || r == '\n'
	})
	if len(first) == 0 {
		return false
	}
	base := strings.ToLower(first[0])

	// Check single-word read-only commands.
	if readOnlyBashCommands[base] {
		return true
	}

	// Check prefix commands (git log, go vet, etc.).
	if len(first) > 1 {
		if sub, ok := readOnlyBashPrefixes[base]; ok {
			return sub[strings.ToLower(first[1])]
		}
	}
	return false
}

// dangerousBashPatterns are glob-like patterns that match destructive
// commands. Used only for a UI warning — the deny list is the actual
// enforcement mechanism.
var dangerousBashPatterns = []struct {
	pattern string
	label   string
}{
	{"rm -rf*", "recursive delete"},
	{"rm -r *", "recursive delete"},
	{"rm -fr*", "recursive delete"},
	{"git push*--force*", "force push"},
	{"git push*-f*", "force push"},
	{"git reset --hard*", "hard reset"},
	{"git clean -f*", "force clean"},
	{"chmod 777*", "world-writable"},
	{"chmod -R 777*", "world-writable recursive"},
	{"chown *", "ownership change"},
	{"sudo *", "superuser"},
	{"mkfs*", "filesystem format"},
	{"dd if=*", "raw device write"},
	{"fdisk*", "partition table"},
	{"> /dev/*", "device overwrite"},
}

// BashDangerWarning returns a short label if subject matches a known
// dangerous pattern, or "" when the command looks safe. This is a visual
// hint only — the Policy rules are the authority.
func BashDangerWarning(subject string) string {
	s := strings.TrimSpace(subject)
	for _, d := range dangerousBashPatterns {
		if matchGlob(d.pattern, s) {
			return d.label
		}
	}
	return ""
}
