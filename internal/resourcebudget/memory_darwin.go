package resourcebudget

import "golang.org/x/sys/unix"

func physicalMemoryMiB() int {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil || n == 0 {
		return 8192
	}
	return int(n >> 20)
}
