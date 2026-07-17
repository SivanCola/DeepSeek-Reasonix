// Copyright 2024–2026 xAI and Reasonix contributors.
// SPDX-License-Identifier: Apache-2.0
//
// Modified/adapted from grok-build grok_build_hashline (commit c68e39f).
// See NOTICE and LICENSE-Apache-2.0 in this directory.

// Copyright adapted from xAI grok-build (Apache-2.0), edit/apply.rs + types.rs.
// Modified for Reasonix Hashline v1.

package hashline

import (
	"fmt"
	"strings"
)

// Op is a single hashline edit operation.
type Op struct {
	// Kind: "replace" | "insert_after" | "write"
	Kind string `json:"op"`

	Anchor    string `json:"anchor,omitempty"`
	EndAnchor string `json:"end_anchor,omitempty"`
	Content   string `json:"content"`
}

// MaxEdits is the maximum number of operations in one batch.
const MaxEdits = 64

const (
	snippetContext       = 3
	maxContiguousSnippet = 80
	recoveryContext      = 5
)

// ErrorKind classifies edit failures.
type ErrorKind string

const (
	ErrAnchorStale      ErrorKind = "anchor_stale"
	ErrAmbiguousAnchor  ErrorKind = "ambiguous_anchor"
	ErrAnchorNotFound   ErrorKind = "anchor_not_found"
	ErrOverlappingEdits ErrorKind = "overlapping_edits"
	ErrInvalidInput     ErrorKind = "invalid_input"
	ErrIO               ErrorKind = "io_error"
)

// EditError is a structured edit failure (no writes applied).
type EditError struct {
	Kind             ErrorKind
	Message          string
	RequestedAnchor  string
	Current          string
	Context          string
	ContextStartLine int
	ShiftedTo        int
	ShiftedAnchor    string
	AmbiguousCands   []int
}

func (e *EditError) Error() string {
	if e == nil {
		return "hashline edit error"
	}
	var b strings.Builder
	b.WriteString(e.Message)
	if e.Context != "" {
		label := "Fresh anchors — use these to retry your edit:"
		if e.ContextStartLine > 0 {
			label = fmt.Sprintf("Fresh anchors around line %d — use these to retry your edit:", e.ContextStartLine)
		}
		b.WriteString("\n\n")
		b.WriteString(label)
		b.WriteByte('\n')
		b.WriteString(e.Context)
	}
	if e.ShiftedAnchor != "" {
		fmt.Fprintf(&b, "\n\nSuggested anchor: %s", e.ShiftedAnchor)
	}
	return b.String()
}

// ApplyResult is the pure outcome of applying edits to content.
type ApplyResult struct {
	// NewContent is set only on success (caller writes to disk).
	NewContent string
	// Snippet is a fresh-anchor window around the edit region.
	Snippet string
	// Warnings are medium/large range cautions.
	Warnings []string
	// Applied is the number of ops applied.
	Applied int
	// Err is set on failure (NewContent empty; zero writes).
	Err *EditError
}

type resolvedOp struct {
	originalIdx int
	start       int // 0-based inclusive
	end         int // 0-based exclusive; insert: start==end
	newLines    []string
}

// ApplyEdits validates all anchors against the pre-edit snapshot, rejects
// overlaps, and applies bottom-up. Any failure yields zero writes (Err set,
// NewContent empty).
func ApplyEdits(content string, ops []Op) ApplyResult {
	if len(ops) == 0 {
		return ApplyResult{Err: &EditError{Kind: ErrInvalidInput, Message: "No edit operations provided."}}
	}
	if len(ops) > MaxEdits {
		return ApplyResult{Err: &EditError{
			Kind:    ErrInvalidInput,
			Message: fmt.Sprintf("Too many edits (%d); maximum is %d.", len(ops), MaxEdits),
		}}
	}

	// Sole write op.
	if len(ops) == 1 && ops[0].Kind == "write" {
		if line := detectAnchorPrefixInContent(ops[0].Content); line > 0 {
			return ApplyResult{Err: anchorContentError("write", ops[0].Content, line)}
		}
		crlf := UsesCRLF(content)
		// Preserve write content as given, but normalize line endings to match file.
		newContent := ops[0].Content
		if crlf && !UsesCRLF(newContent) {
			newContent = toCRLF(newContent)
		}
		snippet := FormatHashlineContent(newContent, 0, snippetContext*2)
		return ApplyResult{
			NewContent: newContent,
			Snippet:    snippet,
			Applied:    1,
		}
	}

	lines := SplitLines(content)
	crlf := UsesCRLF(content)
	resolved := make([]resolvedOp, 0, len(ops))

	for i, op := range ops {
		r, err := resolveOp(op, i, lines)
		if err != nil {
			if len(ops) > 1 {
				err.Message = fmt.Sprintf(
					"Edit %d/%d (%s): %s\n\nThis batch contained %d edits. Because this anchor failed validation, none of the edits were applied. Retry all %d edits with fresh anchors, not just the failed one.",
					i+1, len(ops), op.Kind, err.Message, len(ops), len(ops),
				)
			}
			return ApplyResult{Err: err}
		}
		resolved = append(resolved, r)
	}

	if err := checkOverlaps(resolved); err != nil {
		if len(ops) > 1 {
			err.Message = fmt.Sprintf(
				"%s\n\nThis batch contained %d edits. Because of the overlap, none were applied. Fix the overlapping ranges and retry all edits.",
				err.Message, len(ops),
			)
		}
		return ApplyResult{Err: err}
	}

	var warnings []string
	for _, op := range resolved {
		if w := RangeWarning(op.start, op.end); w != "" {
			warnings = append(warnings, w)
		}
	}

	// Bottom-up + reverse original index → preserve request order for same position.
	sortResolvedBottomUp(resolved)

	result := make([]string, len(lines))
	copy(result, lines)

	// Track edit regions in top-down order for snippets (post-shift coords).
	var regions []editRegion
	var cumulativeShift int
	// Iterate reverse of bottom-up (= top-down) for region tracking.
	for i := len(resolved) - 1; i >= 0; i-- {
		op := resolved[i]
		shiftedStart := op.start + cumulativeShift
		inserted := len(op.newLines)
		regions = append(regions, editRegion{shiftedStart, shiftedStart + inserted})
		cumulativeShift += inserted - (op.end - op.start)
	}

	for _, op := range resolved {
		// splice op.start..op.end with op.newLines
		tail := append([]string{}, result[op.end:]...)
		result = append(result[:op.start], op.newLines...)
		result = append(result, tail...)
	}

	newContent := JoinLines(result, crlf)
	// Recompute total lines via SplitLines for consistency.
	totalNew := len(SplitLines(newContent))
	snippet := buildSnippet(newContent, regions, totalNew)

	return ApplyResult{
		NewContent: newContent,
		Snippet:    snippet,
		Warnings:   warnings,
		Applied:    len(ops),
	}
}

func sortResolvedBottomUp(ops []resolvedOp) {
	// Sort by start descending, then originalIdx descending.
	for i := 0; i < len(ops); i++ {
		for j := i + 1; j < len(ops); j++ {
			if ops[j].start > ops[i].start ||
				(ops[j].start == ops[i].start && ops[j].originalIdx > ops[i].originalIdx) {
				ops[i], ops[j] = ops[j], ops[i]
			}
		}
	}
}

func resolveOp(op Op, originalIdx int, lines []string) (resolvedOp, *EditError) {
	switch op.Kind {
	case "replace":
		start, err := validateAnchor(op.Anchor, lines)
		if err != nil {
			return resolvedOp{}, err
		}
		end := start + 1
		if op.EndAnchor != "" {
			e, err := validateAnchor(op.EndAnchor, lines)
			if err != nil {
				return resolvedOp{}, err
			}
			if e < start {
				return resolvedOp{}, &EditError{
					Kind:            ErrInvalidInput,
					Message:         fmt.Sprintf("end_anchor line %d is before start anchor line %d.", e+1, start+1),
					RequestedAnchor: op.EndAnchor,
				}
			}
			end = e + 1
		}
		if line := detectAnchorPrefixInContent(op.Content); line > 0 {
			return resolvedOp{}, anchorContentError("replace", op.Content, line)
		}
		var newLines []string
		if op.Content != "" {
			newLines = contentToLines(op.Content)
		}
		return resolvedOp{originalIdx: originalIdx, start: start, end: end, newLines: newLines}, nil

	case "insert_after":
		var insertAt int
		switch op.Anchor {
		case "0:":
			insertAt = 0
		case "EOF":
			// Insert before synthetic trailing empty line when file ends with '\n'.
			n := len(lines)
			if n > 1 && lines[n-1] == "" {
				insertAt = n - 1
			} else {
				insertAt = n
			}
		default:
			line, err := validateAnchor(op.Anchor, lines)
			if err != nil {
				return resolvedOp{}, err
			}
			insertAt = line + 1
		}
		if line := detectAnchorPrefixInContent(op.Content); line > 0 {
			return resolvedOp{}, anchorContentError("insert_after", op.Content, line)
		}
		var newLines []string
		if op.Content == "" {
			newLines = []string{""} // blank line
		} else {
			newLines = contentToLines(op.Content)
		}
		return resolvedOp{originalIdx: originalIdx, start: insertAt, end: insertAt, newLines: newLines}, nil

	case "write":
		return resolvedOp{}, &EditError{
			Kind: ErrInvalidInput,
			Message: "Write op must be the only operation in a batch. " +
				"Either use write alone (to replace the entire file) or use replace/insert_after ops without write.",
		}
	default:
		return resolvedOp{}, &EditError{
			Kind:    ErrInvalidInput,
			Message: fmt.Sprintf("Unknown op %q; expected replace, insert_after, or write.", op.Kind),
		}
	}
}

// contentToLines splits model content the same way Rust str::lines does
// (no synthetic trailing empty for a final '\n').
func contentToLines(content string) []string {
	if content == "" {
		return nil
	}
	// Normalize CRLF in model payload to LF for splitting.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	parts := strings.Split(content, "\n")
	// Drop trailing empty if content ended with newline (str::lines behavior).
	if strings.HasSuffix(content, "\n") && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func toCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func validateAnchor(anchorStr string, lines []string) (int, *EditError) {
	// Strip arrow-copied content.
	s := anchorStr
	if i := strings.Index(s, "\u2192"); i >= 0 {
		s = s[:i]
	} else if i := strings.Index(s, "->"); i >= 0 {
		s = s[:i]
	}

	parsed, ok := ParseAnchor(s)
	if !ok {
		// Recovery: hash-only suffix like "ab:cd" when unique.
		if recovered, ok := recoverBySuffix(s, lines); ok {
			parsed = recovered
		} else {
			return 0, &EditError{
				Kind:            ErrInvalidInput,
				Message:         fmt.Sprintf("Malformed anchor: %q. Expected format: \"LINE:HASH1:HASH2\" (e.g. \"22:abc:rst\").", anchorStr),
				RequestedAnchor: anchorStr,
			}
		}
	}

	switch Validate(parsed, lines) {
	case Valid:
		return parsed.Line - 1, nil
	case OutOfRange:
		return 0, &EditError{
			Kind:            ErrAnchorNotFound,
			Message:         fmt.Sprintf("Line %d is out of range (file has %d lines).", parsed.Line, len(lines)),
			RequestedAnchor: anchorStr,
		}
	default: // Stale
		return 0, staleError(parsed, lines, anchorStr)
	}
}

func recoverBySuffix(suffix string, lines []string) (ParsedAnchor, bool) {
	anchors := GenerateAnchors(lines)
	var match *Anchor
	for i := range anchors {
		a := &anchors[i]
		if a.Suffix() == suffix {
			if match != nil {
				return ParsedAnchor{}, false // ambiguous
			}
			match = a
		}
	}
	if match == nil {
		return ParsedAnchor{}, false
	}
	return ParsedAnchor{Line: match.Line, Local: match.Local, Context: match.Context}, true
}

func staleError(parsed ParsedAnchor, lines []string, requested string) *EditError {
	shift := FindShifted(parsed, lines, SearchRadius)
	anchors := GenerateAnchors(lines)

	ctxStart := parsed.Line - 1 - recoveryContext
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := parsed.Line + recoveryContext
	if ctxEnd > len(lines) {
		ctxEnd = len(lines)
	}
	var ctxParts []string
	for i := ctxStart; i < ctxEnd; i++ {
		ctxParts = append(ctxParts, FormatAnchoredLine(anchors[i], lines[i]))
	}
	context := strings.Join(ctxParts, "\n")

	var current string
	idx := parsed.Line - 1
	if idx >= 0 && idx < len(lines) {
		current = FormatAnchoredLine(anchors[idx], lines[idx])
	}

	err := &EditError{
		Kind:             ErrAnchorStale,
		RequestedAnchor:  requested,
		Current:          current,
		Context:          context,
		ContextStartLine: ctxStart + 1,
	}

	switch shift.Kind {
	case "found":
		fresh := anchors[shift.NewLine-1].Render()
		err.ShiftedTo = shift.NewLine
		err.ShiftedAnchor = fresh
		err.Message = fmt.Sprintf(
			"Anchor stale at line %d. Content appears to have shifted to line %d. Retry with anchor %q.",
			parsed.Line, shift.NewLine, fresh,
		)
	case "ambiguous":
		err.Kind = ErrAmbiguousAnchor
		err.AmbiguousCands = shift.Candidates
		err.Message = fmt.Sprintf(
			"Anchor stale at line %d. Multiple candidates at lines %v. Use the fresh anchors from the context below to retry your edit.",
			parsed.Line, shift.Candidates,
		)
	default:
		err.Message = fmt.Sprintf(
			"Anchor stale at line %d. Use the fresh anchors from the context below to retry your edit.",
			parsed.Line,
		)
	}
	return err
}

func checkOverlaps(ops []resolvedOp) *EditError {
	type rng struct{ start, end, idx int }
	var ranges []rng
	for _, op := range ops {
		if op.start != op.end {
			ranges = append(ranges, rng{op.start, op.end, op.originalIdx})
		}
	}
	// sort ranges by start
	for i := 0; i < len(ranges); i++ {
		for j := i + 1; j < len(ranges); j++ {
			if ranges[j].start < ranges[i].start {
				ranges[i], ranges[j] = ranges[j], ranges[i]
			}
		}
	}
	for i := 0; i+1 < len(ranges); i++ {
		if ranges[i].end > ranges[i+1].start {
			return overlapError(ranges[i].start, ranges[i].end, ranges[i].idx, ranges[i+1].start, ranges[i+1].end, ranges[i+1].idx)
		}
	}
	// Insertion inside a replace span.
	for _, op := range ops {
		if op.start != op.end {
			continue
		}
		insertAt := op.start
		for _, r := range ranges {
			if r.start <= insertAt && insertAt < r.end {
				return overlapError(r.start, r.end, r.idx, insertAt, insertAt, op.originalIdx)
			}
		}
	}
	return nil
}

func overlapError(aStart, aEnd, aIdx, bStart, bEnd, bIdx int) *EditError {
	aDesc := formatEditDesc(aStart, aEnd, aIdx)
	bDesc := formatEditDesc(bStart, bEnd, bIdx)
	return &EditError{
		Kind:    ErrOverlappingEdits,
		Message: fmt.Sprintf("Overlapping edits: %s and %s.", aDesc, bDesc),
	}
}

func formatEditDesc(start, end, idx int) string {
	if start == end {
		return fmt.Sprintf("edit #%d (insertion at line %d)", idx+1, start+1)
	}
	return fmt.Sprintf("edit #%d (lines %d-%d)", idx+1, start+1, end)
}

type editRegion struct{ start, end int }

func buildSnippet(newContent string, regions []editRegion, totalNew int) string {
	if len(regions) == 0 {
		return ""
	}
	// Sort regions top-down.
	for i := 0; i < len(regions); i++ {
		for j := i + 1; j < len(regions); j++ {
			if regions[j].start < regions[i].start {
				regions[i], regions[j] = regions[j], regions[i]
			}
		}
	}
	globalStart := regions[0].start - snippetContext
	if globalStart < 0 {
		globalStart = 0
	}
	globalEnd := regions[len(regions)-1].end + snippetContext
	if globalEnd > totalNew {
		globalEnd = totalNew
	}
	if globalEnd-globalStart <= maxContiguousSnippet {
		return FormatHashlineContent(newContent, globalStart, globalEnd-globalStart)
	}

	// Merge overlapping/adjacent regions with context.
	var merged []editRegion
	for _, r := range regions {
		cs := r.start - snippetContext
		if cs < 0 {
			cs = 0
		}
		ce := r.end + snippetContext
		if ce > totalNew {
			ce = totalNew
		}
		if len(merged) > 0 && cs <= merged[len(merged)-1].end {
			if ce > merged[len(merged)-1].end {
				merged[len(merged)-1].end = ce
			}
			continue
		}
		merged = append(merged, editRegion{cs, ce})
	}

	var parts []string
	prevEnd := 0
	for i, m := range merged {
		if i > 0 {
			gap := m.start - prevEnd
			if gap < 0 {
				gap = 0
			}
			parts = append(parts, fmt.Sprintf("... %d lines not shown ...", gap))
		} else if m.start > 0 {
			parts = append(parts, fmt.Sprintf("... %d lines not shown ...", m.start))
		}
		parts = append(parts, FormatHashlineContent(newContent, m.start, m.end-m.start))
		prevEnd = m.end
	}
	if prevEnd < totalNew {
		parts = append(parts, fmt.Sprintf("... %d lines not shown ...", totalNew-prevEnd))
	}
	return strings.Join(parts, "\n")
}

// detectAnchorPrefixInContent rejects replacements that include hashline
// "LINE:HASH→" prefixes copied from read output.
func detectAnchorPrefixInContent(content string) int {
	for idx, line := range strings.Split(content, "\n") {
		s := strings.TrimLeft(line, " \t")
		if before, _, ok := strings.Cut(s, "\u2192"); ok && looksLikeAnchorPrefix(before) {
			return idx + 1
		}
		if before, _, ok := strings.Cut(s, "->"); ok && looksLikeAnchorPrefix(before) {
			return idx + 1
		}
	}
	return 0
}

func looksLikeAnchorPrefix(before string) bool {
	if len(before) == 0 || len(before) > 25 {
		return false
	}
	if !strings.Contains(before, ":") || strings.Contains(before, " ") {
		return false
	}
	return true
}

func anchorContentError(opLabel, content string, lineNum int) *EditError {
	lines := strings.Split(content, "\n")
	offending := ""
	if lineNum-1 >= 0 && lineNum-1 < len(lines) {
		offending = lines[lineNum-1]
	}
	ctxStart := lineNum - 2
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := lineNum + 1
	if ctxEnd > len(lines) {
		ctxEnd = len(lines)
	}
	var ctx []string
	for i := ctxStart; i < ctxEnd; i++ {
		marker := "   "
		if i+1 == lineNum {
			marker = ">>>"
		}
		ctx = append(ctx, fmt.Sprintf("%s line %d: %s", marker, i+1, lines[i]))
	}
	return &EditError{
		Kind:             ErrInvalidInput,
		Message:          fmt.Sprintf("%s content contains anchor prefixes (e.g. \"22:abc:rst\u2192\") copied from hashline_read output. The first offending line is line %d. Strip the anchor prefixes and the \u2192 separator from every line, keeping only the actual file content, then retry.", opLabel, lineNum),
		Current:          offending,
		Context:          strings.Join(ctx, "\n"),
		ContextStartLine: ctxStart + 1,
	}
}
