// Package rulelist splits a list of permission rules written on one line.
//
// Rules are separated by commas or whitespace, and a separator inside
// parentheses belongs to the rule: `Bash(git add:*) Read` is two rules. Skill
// front matter and the --allowed-tools flag share it, so one spelling means
// one thing in both.
package rulelist
