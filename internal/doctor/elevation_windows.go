//go:build windows

package doctor

import (
	"syscall"
	"unsafe"
)

const (
	tokenQuery     = 0x0008
	tokenElevation = 20
)

var (
	advapi32                = syscall.NewLazyDLL("advapi32.dll")
	procOpenProcessToken    = advapi32.NewProc("OpenProcessToken")
	procGetTokenInformation = advapi32.NewProc("GetTokenInformation")
)

type tokenElevationInfo struct {
	IsElevated uint32
}

// isElevated reports whether the current process runs with an elevated token.
// Failure to inspect the token is treated as non-elevated.
func isElevated() bool {
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		return false
	}
	var token syscall.Handle
	ok, _, _ := procOpenProcessToken.Call(uintptr(process), tokenQuery, uintptr(unsafe.Pointer(&token)))
	if ok == 0 {
		return false
	}
	defer syscall.CloseHandle(token)

	var info tokenElevationInfo
	var size uint32
	ok, _, _ = procGetTokenInformation.Call(
		uintptr(token), tokenElevation,
		uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Sizeof(info)),
		uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return false
	}
	return info.IsElevated != 0
}
