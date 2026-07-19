package remote

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ssh_config "github.com/kevinburke/ssh_config"
)

// SSHConfigSource answers per-alias lookups against a parsed OpenSSH client
// config (~/.ssh/config plus Includes). Match blocks are not evaluated by the
// underlying parser; ImportedHost.HasMatchRules is unset because such stanzas
// are simply invisible here — `remote import` notes that limitation.
type SSHConfigSource struct {
	cfg  *ssh_config.Config
	path string
}

// LoadUserSSHConfig parses ~/.ssh/config. A missing file yields an empty
// source (all lookups return zero values), not an error.
func LoadUserSSHConfig() (*SSHConfigSource, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return &SSHConfigSource{}, nil
	}
	return LoadSSHConfig(filepath.Join(home, ".ssh", "config"))
}

// LoadSSHConfig parses one OpenSSH client config file.
func LoadSSHConfig(path string) (*SSHConfigSource, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SSHConfigSource{path: path}, nil
		}
		return nil, err
	}
	defer f.Close()
	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, err
	}
	return &SSHConfigSource{cfg: cfg, path: path}, nil
}

// Path is the file this source was parsed from (may not exist).
func (s *SSHConfigSource) Path() string { return s.path }

func (s *SSHConfigSource) get(alias, key string) string {
	if s == nil || s.cfg == nil {
		return ""
	}
	v, err := s.cfg.Get(alias, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// HostName returns the ssh_config HostName for alias, or "" when it would
// just echo the default/alias back.
func (s *SSHConfigSource) HostName(alias string) string {
	v := s.get(alias, "HostName")
	if v == "" || v == alias {
		return ""
	}
	return v
}

func (s *SSHConfigSource) User(alias string) string { return s.get(alias, "User") }

func (s *SSHConfigSource) Port(alias string) int {
	v := s.get(alias, "Port")
	if v == "" {
		return 0
	}
	p, err := strconv.Atoi(v)
	if err != nil || p <= 0 || p > 65535 || p == 22 {
		return 0
	}
	return p
}

// IdentityFile returns the first non-default identity file, ~-expanded.
func (s *SSHConfigSource) IdentityFile(alias string) string {
	if s == nil || s.cfg == nil {
		return ""
	}
	vals, err := s.cfg.GetAll(alias, "IdentityFile")
	if err != nil || len(vals) == 0 {
		return ""
	}
	v := strings.TrimSpace(vals[0])
	// The parser reports its built-in default (~/.ssh/identity) when the
	// config has no entry; treat it as unset so the auth chain probes the
	// modern default identities instead.
	if v == "" || v == ssh_config.Default("IdentityFile") {
		return ""
	}
	return expandHome(v)
}

func (s *SSHConfigSource) ProxyJump(alias string) string { return s.get(alias, "ProxyJump") }

// ImportedHost is one concrete Host alias surfaced by `remote import`.
type ImportedHost struct {
	Alias        string
	HostName     string
	User         string
	Port         int
	IdentityFile string
	ProxyJump    string
}

// Aliases lists concrete (non-wildcard, non-negated) Host aliases in file
// order, deduplicated, each resolved through the full config.
func (s *SSHConfigSource) Aliases() []ImportedHost {
	if s == nil || s.cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	var order []string
	for _, host := range s.cfg.Hosts {
		for _, pat := range host.Patterns {
			p := pat.String()
			if p == "" || strings.ContainsAny(p, "*?!") || seen[p] {
				continue
			}
			seen[p] = true
			order = append(order, p)
		}
	}
	// File order is meaningful to users, so it is preserved as-is.
	out := make([]ImportedHost, 0, len(order))
	for _, alias := range order {
		out = append(out, ImportedHost{
			Alias:        alias,
			HostName:     s.HostName(alias),
			User:         s.User(alias),
			Port:         s.Port(alias),
			IdentityFile: s.IdentityFile(alias),
			ProxyJump:    s.ProxyJump(alias),
		})
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
