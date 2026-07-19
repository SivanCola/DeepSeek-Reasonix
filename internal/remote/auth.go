package remote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// SecretKind identifies which interactive secret is being requested.
type SecretKind int

const (
	SecretPassphrase SecretKind = iota // private-key passphrase
	SecretPassword                     // password auth
)

func (k SecretKind) String() string {
	if k == SecretPassword {
		return "password"
	}
	return "passphrase"
}

// AuthOptions supplies credential resolution for a dial. Passphrase and
// Password return already-resolved credential-store values (nil when none is
// configured). SecretPrompt is the interactive fallback — a terminal prompt in
// the CLI, a dialog in the desktop — and is only ever called on the first
// connect; reconnects reuse in-memory-cached secrets and never prompt.
type AuthOptions struct {
	Passphrase   func() (string, error)
	Password     func() (string, error)
	SecretPrompt func(ctx context.Context, kind SecretKind, host string) (string, error)
	DisableAgent bool

	// cache holds secrets obtained during the first connect so the supervisor
	// can reconnect silently. Populated by the auth methods.
	cache *secretCache
}

type secretCache struct {
	passphrase string
	password   string
	havePass   bool
	havePw     bool
}

// buildAuthMethods assembles ssh.AuthMethod values in OpenSSH-like order:
// agent, explicit identity file, default identities, password,
// keyboard-interactive. host is the display label used in prompts.
func buildAuthMethods(ctx context.Context, h ResolvedHost, opts *AuthOptions) ([]ssh.AuthMethod, error) {
	if opts.cache == nil {
		opts.cache = &secretCache{}
	}
	var methods []ssh.AuthMethod

	if !opts.DisableAgent {
		if am := agentAuth(); am != nil {
			methods = append(methods, am)
		}
	}

	if h.IdentityFile != "" {
		am, err := keyAuth(ctx, h, opts, h.IdentityFile)
		if err != nil {
			return nil, err
		}
		if am != nil {
			methods = append(methods, am)
		}
	} else {
		for _, path := range defaultIdentityFiles() {
			am, err := keyAuth(ctx, h, opts, path)
			if err != nil {
				// A default key that exists but fails to parse shouldn't abort
				// the whole chain — skip it and try the next.
				continue
			}
			if am != nil {
				methods = append(methods, am)
			}
		}
	}

	methods = append(methods, passwordAuth(ctx, h, opts))
	methods = append(methods, keyboardInteractiveAuth(ctx, h, opts))
	return methods, nil
}

func agentAuth() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		conn, err := dialAgent(sock)
		if err != nil {
			return nil, err
		}
		return agent.NewClient(conn).Signers()
	})
}

// keyAuth loads a private key, resolving a passphrase from the credential
// store then the interactive prompt when the key is encrypted. Returns nil
// (no method, no error) when the key file simply does not exist.
func keyAuth(ctx context.Context, h ResolvedHost, opts *AuthOptions, path string) (ssh.AuthMethod, error) {
	path = expandHome(path)
	pem, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err == nil {
		return ssh.PublicKeys(signer), nil
	}
	var missing *ssh.PassphraseMissingError
	if !isPassphraseMissing(err, &missing) {
		return nil, fmt.Errorf("parse key %s: %w", path, err)
	}
	// Encrypted key: return a lazy method so the passphrase is only resolved
	// if the server actually offers publickey with this key.
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		pass, perr := resolvePassphrase(ctx, h, opts)
		if perr != nil {
			return nil, perr
		}
		s, serr := ssh.ParsePrivateKeyWithPassphrase(pem, []byte(pass))
		if serr != nil {
			return nil, fmt.Errorf("decrypt key %s: %w", path, serr)
		}
		return []ssh.Signer{s}, nil
	}), nil
}

func resolvePassphrase(ctx context.Context, h ResolvedHost, opts *AuthOptions) (string, error) {
	if opts.cache.havePass {
		return opts.cache.passphrase, nil
	}
	if opts.Passphrase != nil {
		if v, err := opts.Passphrase(); err == nil && v != "" {
			opts.cache.passphrase, opts.cache.havePass = v, true
			return v, nil
		}
	}
	if opts.SecretPrompt == nil {
		return "", fmt.Errorf("remote: key passphrase required but no prompt available")
	}
	v, err := opts.SecretPrompt(ctx, SecretPassphrase, h.Label())
	if err != nil {
		return "", err
	}
	opts.cache.passphrase, opts.cache.havePass = v, true
	return v, nil
}

func passwordAuth(ctx context.Context, h ResolvedHost, opts *AuthOptions) ssh.AuthMethod {
	return ssh.RetryableAuthMethod(ssh.PasswordCallback(func() (string, error) {
		return resolvePassword(ctx, h, opts)
	}), 3)
}

func keyboardInteractiveAuth(ctx context.Context, h ResolvedHost, opts *AuthOptions) ssh.AuthMethod {
	return ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range questions {
			// Echoed prompts are typically usernames/OTPs the server labels;
			// non-echoed are passwords. Reuse the cached password for the
			// common single-password challenge.
			pw, err := resolvePassword(ctx, h, opts)
			if err != nil {
				return nil, err
			}
			answers[i] = pw
		}
		return answers, nil
	})
}

func resolvePassword(ctx context.Context, h ResolvedHost, opts *AuthOptions) (string, error) {
	if opts.cache.havePw {
		return opts.cache.password, nil
	}
	if opts.Password != nil {
		if v, err := opts.Password(); err == nil && v != "" {
			opts.cache.password, opts.cache.havePw = v, true
			return v, nil
		}
	}
	if opts.SecretPrompt == nil {
		return "", fmt.Errorf("remote: password required but no prompt available")
	}
	v, err := opts.SecretPrompt(ctx, SecretPassword, h.Label())
	if err != nil {
		return "", err
	}
	opts.cache.password, opts.cache.havePw = v, true
	return v, nil
}

func defaultIdentityFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	names := []string{"id_ed25519", "id_ecdsa", "id_rsa"}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(home, ".ssh", n))
	}
	return out
}

func isPassphraseMissing(err error, target **ssh.PassphraseMissingError) bool {
	if pe, ok := err.(*ssh.PassphraseMissingError); ok {
		*target = pe
		return true
	}
	return false
}
