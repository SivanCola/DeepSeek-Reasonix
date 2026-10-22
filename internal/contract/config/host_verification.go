package config

import "strings"

// WaivesOwedChecks reports whether the host drops the check, review and sign-off
// a changed turn owes. Unset drops them except under the Delivery preset; "off"
// and "on" decide under any preset; any other value reads as "on", so a typo
// never weakens what the host asks.
func (c *Config) WaivesOwedChecks(delivery bool) bool {
	if c == nil {
		return !delivery
	}
	switch strings.ToLower(strings.TrimSpace(c.Agent.HostVerification)) {
	case "":
		return !delivery
	case "off":
		return true
	default:
		return false
	}
}
