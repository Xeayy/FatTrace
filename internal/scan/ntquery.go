//go:build windows

package scan

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	fileIDBothDirectoryInformation = 37
	statusNoMoreFiles              = 0x80000006
	statusBufferOverflow           = 0x80000005

	fileListDirectory        = 0x00000001
	fileReadAttributes       = 0x00000080
	fileFlagBackupSemantics  = 0x02000000
	fileFlagOpenReparsePoint = 0x00200000
	fileAttributeDirectory   = 0x00000010
	fileAttributeReparse     = 0x00000400

	queryBufferSize      = 256 * 1024
	fileInfoStructSize   = 104
	filetimeEpochOffset  = 11644473600
	filetimeTicksPerSec  = 10_000_000
	filetimeTicksPerNano = 100
)

var (
	ntdll                     = syscall.NewLazyDLL("ntdll.dll")
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procNtQueryDirectoryFile  = ntdll.NewProc("NtQueryDirectoryFile")
	procRtlNtStatusToDosError = ntdll.NewProc("RtlNtStatusToDosError")
	procGetDiskFreeSpaceEx    = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetVolumeInformation  = kernel32.NewProc("GetVolumeInformationW")
)

type ioStatusBlock struct {
	Status      uintptr
	Information uintptr
}

// FILE_ID_BOTH_DIR_INFORMATION (x64). The variable-length FileName follows at offset 104.
type fileIDBothDirInfo struct {
	NextEntryOffset uint32
	FileIndex       uint32
	CreationTime    int64
	LastAccessTime  int64
	LastWriteTime   int64
	ChangeTime      int64
	EndOfFile       int64
	AllocationSize  int64
	FileAttributes  uint32
	FileNameLength  uint32
	EaSize          uint32
	ShortNameLength int8
	ShortName       [12]uint16
	FileID          int64
}

func openDirHandle(path string) (syscall.Handle, error) {
	return openHandle(path, fileListDirectory|fileReadAttributes)
}

func openFileHandle(path string) (syscall.Handle, error) {
	return openHandle(path, fileReadAttributes)
}

func openHandle(path string, access uint32) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(toLongPath(path))
	if err != nil {
		return 0, err
	}
	h, err := syscall.CreateFile(
		p,
		access,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		fileFlagBackupSemantics|fileFlagOpenReparsePoint,
		0,
	)
	if err != nil {
		return 0, err
	}
	return h, nil
}

func handleInfo(h syscall.Handle) (syscall.ByHandleFileInformation, error) {
	var info syscall.ByHandleFileInformation
	err := syscall.GetFileInformationByHandle(h, &info)
	return info, err
}

func handleIdentity(h syscall.Handle) (frn uint64, serial uint32, err error) {
	info, err := handleInfo(h)
	if err != nil {
		return 0, 0, err
	}
	frn = uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	return frn, info.VolumeSerialNumber, nil
}

func toLongPath(p string) string {
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) {
		return p
	}
	if !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	if strings.HasPrefix(p, `\\`) {
		return p
	}
	return `\\?\` + p
}

func filetimeToTime(ft int64) time.Time {
	if ft == 0 {
		return time.Time{}
	}
	sec := ft/filetimeTicksPerSec - filetimeEpochOffset
	nsec := (ft % filetimeTicksPerSec) * filetimeTicksPerNano
	return time.Unix(sec, nsec)
}

func ntStatusError(status uint32) error {
	r, _, _ := procRtlNtStatusToDosError.Call(uintptr(status))
	if r != 0 {
		return syscall.Errno(r)
	}
	return fmt.Errorf("NTSTATUS 0x%08X", status)
}

// GetVolumeSpace returns total, free and caller-available bytes for the volume containing path.
func GetVolumeSpace(path string) (total, free, available int64, err error) {
	root := volumeRoot(path)
	if root == "" {
		return 0, 0, 0, errors.New("path has no volume")
	}
	p, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0, 0, 0, err
	}
	var avail, totalBytes, totalFree uint64
	r1, _, _ := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&avail)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r1 == 0 {
		return 0, 0, 0, errors.New("GetDiskFreeSpaceExW failed")
	}
	return int64(totalBytes), int64(totalFree), int64(avail), nil
}

func volumeRoot(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	vol := filepath.VolumeName(abs)
	if len(vol) != 2 || vol[1] != ':' {
		return ""
	}
	return vol + `\`
}

type VolumeMeta struct {
	Root   string
	Volume string
	Serial uint32
	FSType string
	Label  string
	IsRoot bool
}

// VolumeMetadata identifies the volume containing root and returns its serial,
// file system and label. IsRoot reports whether root is the volume root.
func VolumeMetadata(root string) (VolumeMeta, error) {
	abs, err := normalizeRoot(root)
	if err != nil {
		return VolumeMeta{}, err
	}
	vol := volumeRoot(abs)
	if vol == "" {
		return VolumeMeta{}, fmt.Errorf("unsupported path %q", abs)
	}
	h, err := openDirHandle(abs)
	if err != nil {
		return VolumeMeta{}, fmt.Errorf("open root %s: %w", abs, err)
	}
	_, serial, err := handleIdentity(h)
	syscall.CloseHandle(h)
	if err != nil {
		return VolumeMeta{}, fmt.Errorf("identify root %s: %w", abs, err)
	}

	var label, fsName [256]uint16
	var maxComponent, fsFlags uint32
	p, err := syscall.UTF16PtrFromString(vol)
	if err != nil {
		return VolumeMeta{}, err
	}
	r, _, callErr := procGetVolumeInformation.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&label[0])), uintptr(len(label)),
		0,
		uintptr(unsafe.Pointer(&maxComponent)),
		uintptr(unsafe.Pointer(&fsFlags)),
		uintptr(unsafe.Pointer(&fsName[0])), uintptr(len(fsName)),
	)
	if r == 0 {
		return VolumeMeta{}, fmt.Errorf("GetVolumeInformationW %s: %w", vol, callErr)
	}
	return VolumeMeta{
		Root:   abs,
		Volume: vol,
		Serial: serial,
		FSType: syscall.UTF16ToString(fsName[:]),
		Label:  syscall.UTF16ToString(label[:]),
		IsRoot: strings.EqualFold(abs, vol),
	}, nil
}
