// Package hashline implements Hashline v1: whitespace-normalized FNV-1a line
// anchors and the three tools hashline_read, hashline_edit, and hashline_grep.
//
// Adapted from xAI grok-build (Apache-2.0), commit c68e39f, package
// grok_build_hashline. See NOTICE in this directory.
//
// Tools are NOT registered via init() — classic sessions must not see them in
// tool.BuiltinContractEntries. Boot adds them only for hashline-protocol
// sessions via NewRead / NewEdit / NewGrep or Tools().
//
// Anchor v1 fixed scheme (no runtime schema parameters):
//   - whitespace-normalized FNV-1a 32-bit line hash
//   - lowercase letter encoding, local hash length 3
//   - chunk fingerprint length 3, chunk size 8
//   - display format LINE:LOCAL:CHUNK→CONTENT
//   - shifted recovery radius ±15 lines
//   - special anchors: "EOF", "0:" (before first line)
package hashline
