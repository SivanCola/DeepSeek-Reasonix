// Package installsource implements the `install_source` tool: a two-phase
// installer for Reasonix skills and MCP servers. A single call resolves a
// source (URL, local file/folder, .mcp.json, package name, or local executable)
// into a deterministic plan, then — only when the caller sets apply=true and
// any registered ApprovalFunc consents — performs the writes and MCP connects.
//
// The two-phase design exists so the model (or a UI) can inspect a plan before
// it touches disk or spawns subprocesses. `install_source` deliberately does
// not run a README's `curl | sh` chain: it locates a concrete manifest
// (SKILL.md / <name>.md / <name>/SKILL.md / .mcp.json / mcpServers entry) and
// describes what it would do, and only then does it act on apply=true.
//
// Concurrency: each Execute call is independent; the tool does not lock the
// filesystem. Callers that want to serialize installs (e.g. two parallel calls
// for the same skill name) should do so in the host.
package installsource
