// Copyright 2024–2026 xAI and Reasonix contributors.
// SPDX-License-Identifier: Apache-2.0
//
// Modified/adapted from grok-build grok_build_hashline (commit c68e39f).
// See NOTICE and LICENSE-Apache-2.0 in this directory.

// Copyright adapted from xAI grok-build (Apache-2.0), scheme.rs / anchor.rs.
// Modified for Reasonix Hashline v1 fixed constants.

package hashline

import (
	"fmt"
	"strconv"
	"strings"
)

// Fixed Anchor v1 parameters (not runtime-configurable).
const (
	// ChunkSize is the fixed chunk window for chunk fingerprints.
	ChunkSize = 8
	// SearchRadius is the ± line window for shifted-anchor recovery.
	SearchRadius = 15
	// SchemeName is the machine-readable scheme id.
	SchemeName = "chunk_v1"
)

// Anchor is a rendered anchor for a single line.
type Anchor struct {
	Line    int    // 1-based
	Local   string // e.g. "abc"
	Context string // chunk fingerprint; always set for v1
}

// Render returns "LINE:LOCAL:CHUNK".
func (a Anchor) Render() string {
	return fmt.Sprintf("%d:%s:%s", a.Line, a.Local, a.Context)
}

// Suffix returns "LOCAL:CHUNK" (without line number).
func (a Anchor) Suffix() string {
	return a.Local + ":" + a.Context
}

// ParsedAnchor is a model-supplied anchor after parse.
type ParsedAnchor struct {
	Line    int
	Local   string
	Context string // empty when omitted
}

// Render returns the canonical string form.
func (p ParsedAnchor) Render() string {
	if p.Context == "" {
		return fmt.Sprintf("%d:%s", p.Line, p.Local)
	}
	return fmt.Sprintf("%d:%s:%s", p.Line, p.Local, p.Context)
}

// ValidationResult is the outcome of validating an anchor.
type ValidationResult int

const (
	Valid ValidationResult = iota
	Stale
	OutOfRange
)

// ShiftResult is the outcome of shifted-anchor recovery.
type ShiftResult struct {
	// Kind: "found", "ambiguous", "not_found"
	Kind       string
	NewLine    int   // 1-based when Kind=="found"
	Candidates []int // 1-based when Kind=="ambiguous"
}

// ParseAnchor parses "22:abc" or "22:abc:rst". Returns ok=false if malformed.
// Line 0 is rejected (use the special "0:" insert anchor separately).
func ParseAnchor(s string) (ParsedAnchor, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ParsedAnchor{}, false
	}
	// Strip accidentally copied "→content" / "->content".
	if i := strings.Index(s, "\u2192"); i >= 0 {
		s = s[:i]
	} else if i := strings.Index(s, "->"); i >= 0 {
		s = s[:i]
	}

	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return ParsedAnchor{}, false
	}
	lineStr, local := parts[0], parts[1]
	if lineStr == "" || local == "" {
		return ParsedAnchor{}, false
	}
	line, err := strconv.Atoi(lineStr)
	if err != nil || line <= 0 {
		return ParsedAnchor{}, false
	}
	if !isLowerLetters(local) {
		return ParsedAnchor{}, false
	}
	ctx := ""
	if len(parts) == 3 {
		ctx = parts[2]
		if ctx == "" || !isLowerLetters(ctx) {
			return ParsedAnchor{}, false
		}
	}
	return ParsedAnchor{Line: line, Local: local, Context: ctx}, true
}

func isLowerLetters(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

// SplitLines splits file content into logical lines without trailing newlines.
// A trailing newline produces a final empty entry (so "a\n" → ["a", ""]).
// Empty content yields a single empty line.
// CRLF is accepted; the trailing '\r' is stripped from each line body.
func SplitLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] != '\n' {
			continue
		}
		line := content[start:i]
		if strings.HasSuffix(line, "\r") {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
		start = i + 1
	}
	if start < len(content) {
		line := content[start:]
		if strings.HasSuffix(line, "\r") {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
	} else {
		// content ended with '\n' — synthetic trailing empty line
		lines = append(lines, "")
	}
	return lines
}

// UsesCRLF reports whether content primarily uses CRLF line endings.
func UsesCRLF(content string) bool {
	return strings.Contains(content, "\r\n")
}

// JoinLines joins lines with LF or CRLF. A trailing empty line produces a
// trailing newline (matching SplitLines round-trip).
func JoinLines(lines []string, crlf bool) string {
	if len(lines) == 0 {
		return ""
	}
	sep := "\n"
	if crlf {
		sep = "\r\n"
	}
	return strings.Join(lines, sep)
}

// GenerateAnchors builds Anchor v1 (chunk) anchors for every line.
func GenerateAnchors(lines []string) []Anchor {
	n := len(lines)
	if n == 0 {
		return nil
	}
	numChunks := (n + ChunkSize - 1) / ChunkSize
	chunkFPs := make([]string, numChunks)
	for c := 0; c < numChunks; c++ {
		start := c * ChunkSize
		end := start + ChunkSize
		if end > n {
			end = n
		}
		combined := FNV1a32([]byte("chunk"))
		for _, line := range lines[start:end] {
			lh := LineHash(line)
			combined ^= lh
			combined *= fnvPrime
		}
		chunkFPs[c] = EncodeHash(combined, DefaultHashLen)
	}

	out := make([]Anchor, n)
	for i, line := range lines {
		h := LineHash(line)
		ci := i / ChunkSize
		out[i] = Anchor{
			Line:    i + 1,
			Local:   EncodeHash(h, DefaultHashLen),
			Context: chunkFPs[ci],
		}
	}
	return out
}

// Validate checks a parsed anchor against the current lines.
func Validate(anchor ParsedAnchor, lines []string) ValidationResult {
	idx := anchor.Line - 1
	if idx < 0 || idx >= len(lines) {
		return OutOfRange
	}
	expectedLocal := EncodeHash(LineHash(lines[idx]), DefaultHashLen)
	if anchor.Local != expectedLocal {
		return Stale
	}
	// Chunk scheme requires context — truncated anchors are stale.
	if anchor.Context == "" {
		return Stale
	}
	if anchor.Context != chunkFingerprint(lines, idx) {
		return Stale
	}
	return Valid
}

func chunkFingerprint(lines []string, lineIdx int) string {
	start := (lineIdx / ChunkSize) * ChunkSize
	end := start + ChunkSize
	if end > len(lines) {
		end = len(lines)
	}
	combined := FNV1a32([]byte("chunk"))
	for _, line := range lines[start:end] {
		lh := LineHash(line)
		combined ^= lh
		combined *= fnvPrime
	}
	return EncodeHash(combined, DefaultHashLen)
}

// FindShifted searches ±SearchRadius (or custom radius) for a unique match.
// Never auto-applies; callers surface Found/Ambiguous/NotFound to the model.
func FindShifted(anchor ParsedAnchor, lines []string, radius int) ShiftResult {
	if radius <= 0 {
		radius = SearchRadius
	}
	origIdx := anchor.Line - 1
	if origIdx < 0 {
		origIdx = 0
	}
	start := origIdx - radius
	if start < 0 {
		start = 0
	}
	end := origIdx + radius + 1
	if end > len(lines) {
		end = len(lines)
	}

	var candidates []int
	for idx := start; idx < end; idx++ {
		if idx == origIdx {
			continue
		}
		local := EncodeHash(LineHash(lines[idx]), DefaultHashLen)
		if local != anchor.Local {
			continue
		}
		if anchor.Context != "" {
			probe := ParsedAnchor{Line: idx + 1, Local: local, Context: anchor.Context}
			if Validate(probe, lines) != Valid {
				continue
			}
		}
		candidates = append(candidates, idx+1)
	}
	switch len(candidates) {
	case 0:
		return ShiftResult{Kind: "not_found"}
	case 1:
		return ShiftResult{Kind: "found", NewLine: candidates[0]}
	default:
		return ShiftResult{Kind: "ambiguous", Candidates: candidates}
	}
}

// FormatAnchoredLine returns "LINE:LOCAL:CHUNK→CONTENT".
func FormatAnchoredLine(a Anchor, content string) string {
	return fmt.Sprintf("%s\u2192%s", a.Render(), content)
}

// FormatHashlineContent formats a window of lines with anchors.
// offset is 0-based line offset; limit is max lines (0 = all remaining).
func FormatHashlineContent(content string, offset, limit int) string {
	lines := SplitLines(content)
	anchors := GenerateAnchors(lines)
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(lines)
	}
	var b strings.Builder
	written := 0
	for i := offset; i < len(lines) && written < limit; i++ {
		if written > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(FormatAnchoredLine(anchors[i], lines[i]))
		written++
	}
	return b.String()
}
