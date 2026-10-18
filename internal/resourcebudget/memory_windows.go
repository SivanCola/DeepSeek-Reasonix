package resourcebudget

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func physicalMemoryMiB() int {
	var status struct {
		Length, Load                                                                     uint32
		TotalPhys, AvailPhys, TotalPage, AvailPage, TotalVirtual, AvailVirtual, Extended uint64
	}
	status.Length = uint32(unsafe.Sizeof(status))
	fn := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	if ok, _, _ := fn.Call(uintptr(unsafe.Pointer(&status))); ok == 0 {
		return 8192
	}
	return max(1, int(status.TotalPhys>>20))
}
