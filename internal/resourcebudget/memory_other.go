//go:build !darwin && !linux && !windows

package resourcebudget

func physicalMemoryMiB() int { return 8192 }
