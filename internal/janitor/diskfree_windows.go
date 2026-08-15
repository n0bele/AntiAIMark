//go:build windows

package janitor

import (
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// diskFreePercent returns free space as a percentage of volume capacity for
// the volume containing path, via GetDiskFreeSpaceExW.
func diskFreePercent(path string) (float64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, _totalFree uint64
	r1, _, _ := procGetDiskFreeSpaceW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&_totalFree)),
	)
	if r1 == 0 {
		return 0, syscall.GetLastError()
	}
	if total == 0 {
		return 100, nil
	}
	return float64(freeToCaller) * 100 / float64(total), nil
}
