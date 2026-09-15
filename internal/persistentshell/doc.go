// Package persistentshell runs foreground bash commands in a session-scoped
// PTY so cwd, exported variables, and shell functions survive across calls.
//
// It is invisible to models: the bash tool schema and description stay
// byte-identical. Background jobs, per-call write-root escalations, and host
// terminals keep using one-shot processes.
package persistentshell
