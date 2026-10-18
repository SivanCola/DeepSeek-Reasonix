package resourcebudget

import "golang.org/x/sys/unix"

func physicalMemoryMiB() int {
	var info unix.Sysinfo_t
	if unix.Sysinfo(&info) != nil {
		return 8192
	}
	return max(1, int(uint64(info.Totalram)*uint64(info.Unit)>>20))
}
