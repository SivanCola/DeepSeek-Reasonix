//go:build linux && cgo && reasonix_webkit_smoke

package main

import "testing"

func TestWebKitNativeRecoverySmoke(t *testing.T) {
	tests := []struct {
		name       string
		mode       int
		recoveries []int
		reloads    int
	}{
		{name: "success", mode: webKitSmokeSuccess, recoveries: []int{webKitRecoverySucceeded}, reloads: 1},
		{name: "failure", mode: webKitSmokeFailure, recoveries: []int{webKitRecoveryFailed}, reloads: 1},
		{name: "timeout", mode: webKitSmokeTimeout, recoveries: []int{webKitRecoveryFailed}, reloads: 1},
		{name: "cooldown", mode: webKitSmokeCooldown, recoveries: []int{webKitRecoverySucceeded, webKitRecoveryNotApplicable}, reloads: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, events, reloads := runWebKitNativeSmoke(test.mode)
			if result != 0 {
				t.Fatalf("native smoke result = %d", result)
			}
			if reloads != test.reloads {
				t.Fatalf("reload count = %d, want %d", reloads, test.reloads)
			}
			if len(events) != len(test.recoveries) {
				t.Fatalf("events = %+v, want %d", events, len(test.recoveries))
			}
			for i, event := range events {
				if event.reason != 2 {
					t.Errorf("event %d reason = %d, want terminated_by_api", i, event.reason)
				}
				if event.recovery != test.recoveries[i] {
					t.Errorf("event %d recovery = %d, want %d", i, event.recovery, test.recoveries[i])
				}
			}
		})
	}
}
