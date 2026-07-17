// Copyright 2024–2026 xAI and Reasonix contributors.
// SPDX-License-Identifier: Apache-2.0
//
// Modified/adapted from grok-build grok_build_hashline (commit c68e39f).
// See NOTICE and LICENSE-Apache-2.0 in this directory.

// Copyright adapted from xAI grok-build (Apache-2.0), util/hash.rs.
// Modified for Reasonix Hashline v1.

package hashline

import "strings"

// FNV-1a 32-bit constants.
const (
	fnvOffset = 2166136261
	fnvPrime  = 16777619
)

// DefaultHashLen is the fixed Anchor v1 local/chunk hash length.
const DefaultHashLen = 3

// FNV1a32 computes the FNV-1a 32-bit hash of raw bytes.
func FNV1a32(data []byte) uint32 {
	h := uint32(fnvOffset)
	for _, b := range data {
		h ^= uint32(b)
		h *= fnvPrime
	}
	return h
}

// LineHash computes a whitespace-normalized FNV-1a 32-bit fingerprint of a line.
// Leading/trailing Unicode whitespace is trimmed (strings.TrimSpace / Rust
// str::trim); internal ASCII whitespace runs collapse to a single ASCII space.
func LineHash(line string) uint32 {
	trimmed := strings.TrimSpace(line)
	h := uint32(fnvOffset)
	prevWS := false
	for i := 0; i < len(trimmed); i++ {
		b := trimmed[i]
		if isASCIIWhitespace(b) {
			if !prevWS {
				h ^= uint32(' ')
				h *= fnvPrime
				prevWS = true
			}
			continue
		}
		h ^= uint32(b)
		h *= fnvPrime
		prevWS = false
	}
	return h
}

// EncodeHash encodes a 32-bit hash as n lowercase ASCII letters (a–z).
// len must be in 1..=4 (Anchor v1 uses 3).
func EncodeHash(hash uint32, length int) string {
	if length < 1 || length > 4 {
		length = DefaultHashLen
	}
	buf := make([]byte, length)
	for i := 0; i < length; i++ {
		buf[i] = byte((hash>>(i*8))%26) + 'a'
	}
	return string(buf)
}

func isASCIIWhitespace(b byte) bool {
	// Matches Rust u8::is_ascii_whitespace: space, tab, LF, FF, CR, VT.
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}
