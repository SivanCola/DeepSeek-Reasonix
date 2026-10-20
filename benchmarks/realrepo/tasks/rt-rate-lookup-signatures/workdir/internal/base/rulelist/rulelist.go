package rulelist

import (
	"errors"
	"strings"
	"unicode"
)

var (
	// ErrUnclosed: an opening parenthesis is never closed.
	ErrUnclosed = errors.New("unclosed '('")
	// ErrUnexpectedClose: a closing parenthesis has no opening one.
	ErrUnexpectedClose = errors.New("unexpected ')'")
)

// Split returns the rules in value, in order, trimmed and non-empty.
func Split(value string) ([]string, error) {
	var rules []string
	start, depth := -1, 0
	flush := func(end int) {
		if start < 0 {
			return
		}
		if rule := strings.TrimSpace(value[start:end]); rule != "" {
			rules = append(rules, rule)
		}
		start = -1
	}
	for i, r := range value {
		switch r {
		case '(':
			if start < 0 {
				start = i
			}
			depth++
		case ')':
			if depth == 0 {
				return nil, ErrUnexpectedClose
			}
			depth--
		default:
			if depth == 0 && (r == ',' || unicode.IsSpace(r)) {
				flush(i)
				continue
			}
			if start < 0 {
				start = i
			}
		}
	}
	if depth != 0 {
		return nil, ErrUnclosed
	}
	flush(len(value))
	return rules, nil
}
