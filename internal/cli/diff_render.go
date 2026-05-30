package cli

import "strings"

func isUnifiedDiff(text string) bool {
	hasHunk := strings.Contains(text, "@@")
	hasPlus := strings.Contains(text, "\n+")
	hasMinus := strings.Contains(text, "\n-")
	return hasHunk && (hasPlus || hasMinus)
}

func colorizeDiff(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "@@") {
			lines[i] = dim(line)
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			lines[i] = green(line)
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			lines[i] = red(line)
		}
	}
	return strings.Join(lines, "\n")
}
