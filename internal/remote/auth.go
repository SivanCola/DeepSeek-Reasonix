package remote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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

// SecretPrompt obtains a one-shot credential without persisting or publishing
// it. Implementations should respect ctx cancellation when the connection is
// stopped or superseded.
type SecretPrompt func(ctx context.Context, kind SecretKind, host string) (string, error)

// AuthOptions supplies credential resolution for a dial. Passphrase and
// Password return already-resolved credential-store values (nil when none is
// configured). SecretPrompt is the interactive fallback — a terminal prompt in
// the CLI, a dialog in the desktop — and is only ever called on the first
// connect; reconnects reuse in-memory-cached secrets and never prompt.
type AuthOptions struct {
	Passphrase   func() (string, error)
	Password     func() (string, error)
	SecretPrompt SecretPrompt
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

// buildAuthMethods assembles authentication in OpenSSH-like order: agent,
// explicit identity file (or default identities), password, then
// keyboard-interactive. Public-key sources are returned through an AuthCallback
// because x/crypto/ssh deliberately uses only the first static AuthMethod for a
// protocol method. Without the callback, an empty or rejected agent consumes
// "publickey" and the configured identity file is never attempted.
//
// Password methods are only offered when a stored credential or interactive
// prompt exists. Otherwise a rejected public key must remain a public-key
// authentication failure instead of being masked by a misleading "password
// required but no prompt available" callback error.
func buildAuthMethods(ctx context.Context, h ResolvedHost, opts *AuthOptions) ([]ssh.AuthMethod, ssh.ClientAuthCallback, func(), error) {
	if opts.cache == nil {
		opts.cache = &secretCache{}
	}
	var publicKeys []ssh.AuthMethod
	var fallback []ssh.AuthMethod
	cleanup := func() {}

	if !opts.DisableAgent {
		if am, closeAgent := agentAuth(); am != nil {
			publicKeys = append(publicKeys, am)
			cleanup = closeAgent
		}
	}

	identityFiles := append([]string(nil), h.IdentityFiles...)
	if len(identityFiles) == 0 && h.IdentityFile != "" {
		identityFiles = []string{h.IdentityFile}
	}
	if len(identityFiles) > 0 {
		for _, identityFile := range identityFiles {
			am, err := keyAuth(ctx, h, opts, identityFile)
			if err != nil {
				// Preserve the old explicit-single-key behavior, but let an
				// OpenSSH identity list continue to its remaining candidates.
				if len(identityFiles) == 1 {
					cleanup()
					return nil, nil, func() {}, err
				}
				continue
			}
			if am != nil {
				publicKeys = append(publicKeys, am)
			}
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
				publicKeys = append(publicKeys, am)
			}
		}
	}

	if opts.Password != nil || opts.SecretPrompt != nil {
		fallback = append(fallback, passwordAuth(ctx, h, opts))
		fallback = append(fallback, keyboardInteractiveAuth(ctx, h, opts))
	}
	return fallback, publicKeyAuthCallback(publicKeys), cleanup, nil
}

// publicKeyAuthCallback returns each public-key source exactly once while the
// server continues to allow publickey authentication. AuthCallback may return
// multiple AuthMethod values with the same protocol name, unlike ClientConfig's
// static Auth slice.
func publicKeyAuthCallback(methods []ssh.AuthMethod) ssh.ClientAuthCallback {
	if len(methods) == 0 {
		return nil
	}
	next := 0
	return func(ctx *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
		if next >= len(methods) || !containsAuthMethod(ctx.AllowedMethods, "publickey") {
			return nil, nil
		}
		method := methods[next]
		next++
		return method, nil
	}
}

func containsAuthMethod(methods []string, want string) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}

func agentAuth() (ssh.AuthMethod, func()) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, func() {}
	}
	var mu sync.Mutex
	var conns []interface{ Close() error }
	method := ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		conn, err := dialAgent(sock)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		conns = append(conns, conn)
		mu.Unlock()
		return agent.NewClient(conn).Signers()
	})
	return method, func() {
		mu.Lock()
		owned := conns
		conns = nil
		mu.Unlock()
		for _, conn := range owned {
			_ = conn.Close()
		}
	}
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
		v, err := opts.Passphrase()
		if err != nil {
			return "", err
		}
		if v != "" {
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
		// Never copy a password into echoed, OTP, or multi-question prompts.
		// The current callback models only a password secret, so support the
		// common single hidden-password challenge and fail closed otherwise.
		if len(questions) != 1 || len(echos) != 1 || echos[0] {
			return nil, fmt.Errorf("remote: unsupported keyboard-interactive challenge from %s", h.Label())
		}
		pw, err := resolvePassword(ctx, h, opts)
		if err != nil {
			return nil, err
		}
		return []string{pw}, nil
	})
}

func resolvePassword(ctx context.Context, h ResolvedHost, opts *AuthOptions) (string, error) {
	if opts.cache.havePw {
		return opts.cache.password, nil
	}
	if opts.Password != nil {
		v, err := opts.Password()
		if err != nil {
			return "", err
		}
		if v != "" {
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
