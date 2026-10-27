// Package hostframe renders data the host hands the model from someone other
// than the user, under a tag that says whose it is. The body can neither close
// its block nor open a second one, and names the sender chose are reduced to
// printable tokens before they reach an attribute.
package hostframe

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// Attr is one attribute of a block's opening tag, rendered as name="value".
type Attr struct{ Name, Value string }

var defusers sync.Map

func defuser(tag string) *regexp.Regexp {
	if re, ok := defusers.Load(tag); ok {
		return re.(*regexp.Regexp)
	}
	re, _ := defusers.LoadOrStore(tag, regexp.MustCompile(`(?i)<(\s*/?\s*`+regexp.QuoteMeta(tag)+`)`))
	return re.(*regexp.Regexp)
}

// Block renders body as "<tag a="v">\nbody\n</tag>\n". Every opening or closing
// of tag inside body has its '<' escaped as "<\".
func Block(tag string, attrs []Attr, body string) string {
	var b strings.Builder
	b.WriteString("<" + tag)
	for _, a := range attrs {
		fmt.Fprintf(&b, " %s=%q", a.Name, a.Value)
	}
	b.WriteString(">\n")
	b.WriteString(defuser(tag).ReplaceAllString(body, `<\$1`))
	b.WriteString("\n</" + tag + ">\n")
	return b.String()
}

// Token reduces a name someone other than the host chose to at most limit
// letters, digits, spaces and "._-".
func Token(s string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if n >= limit {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune("._-", r) {
			b.WriteRune(r)
			n++
		}
	}
	return strings.TrimSpace(b.String())
}
