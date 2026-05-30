package cli

import (
	"os"
	"strconv"

	"golang.org/x/term"
)

// colorEnabled is decided once at startup: only colorize when writing to a real
// terminal and the user hasn't opted out via NO_COLOR (https://no-color.org) or
// a dumb TERM. Piped/redirected output and CI stay plain so scripts aren't broken.
var colorEnabled = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiReverse = "\033[7m"
	// ansiAccent is reasonix's brand colour: a warm copper (xterm-256 #173) chosen
	// to read on both dark and light terminals. It marks titles, prompts, and
	// box borders so the eye finds the same hue every time.
	ansiAccent = "\033[38;5;173m"
)

func sgr(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + ansiReset
}

func bold(s string) string    { return sgr(ansiBold, s) }
func dim(s string) string     { return sgr(ansiDim, s) }
func red(s string) string     { return sgr(ansiRed, s) }
func green(s string) string   { return sgr(ansiGreen, s) }
func yellow(s string) string  { return sgr(ansiYellow, s) }
func accent(s string) string  { return sgr(ansiAccent, s) }
func reverse(s string) string { return sgr(ansiReverse, s) }

// isLightTerminal detects whether the terminal has a light background by reading
// the COLORFGBG environment variable. On many terminals this is set to "fg;bg"
// where each is an xterm color number (0–15). A background of 7 or higher
// (and not 0) is considered light. Defaults to dark when COLORFGBG is empty,
// unparseable, the terminal is not a TTY, or colour is disabled.
func isLightTerminal() bool {
	if !colorEnabled {
		return false
	}
	s := os.Getenv("COLORFGBG")
	if s == "" {
		return false
	}
	// Locate the semicolon separating fg from bg.
	semi := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			semi = i
			break
		}
	}
	if semi < 0 || semi+1 >= len(s) {
		return false
	}
	bg, err := strconv.Atoi(s[semi+1:])
	if err != nil {
		return false
	}
	return bg >= 7
}

// UserBubbleBg returns the XTerm colour string for user-message bubble background.
func UserBubbleBg() string {
	if isLightTerminal() {
		return "252"
	}
	return "236"
}

// CompMenuBg returns the XTerm colour string for autocomplete menu background.
func CompMenuBg() string {
	if isLightTerminal() {
		return "254"
	}
	return "234"
}
