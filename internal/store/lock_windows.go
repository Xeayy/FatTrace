//go:build windows

package store

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

// fileLock is a process-wide exclusive lock implemented as a lock file opened
// with dwShareMode=0. Any second writer (same or different process) fails with
// ERROR_SHARING_VIOLATION; the handle is closed automatically by the OS if the
// process dies, so a crash never leaves a stale lock.
type fileLock struct {
	h syscall.Handle
}

const errorSharingViolation syscall.Errno = 32

func acquireLock(path string) (*fileLock, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(
		p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // no sharing: exclusive
		nil,
		syscall.CREATE_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if errors.Is(err, errorSharingViolation) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	_, _ = syscall.Write(h, []byte("pid="+strconv.Itoa(os.Getpid())+"\n"))
	return &fileLock{h: h}, nil
}

// acquireLockWait retries a busy writer lock with backoff. It is separate from
// acquireLock so interactive commands still fail fast while scheduled runs can
// queue behind a catch-up scan.
func acquireLockWait(path string, timeout time.Duration) (*fileLock, error) {
	const (
		initial = 250 * time.Millisecond
		maxWait = 2 * time.Second
	)
	deadline := time.Now().Add(timeout)
	wait := initial
	for {
		lk, err := acquireLock(path)
		if err == nil {
			return lk, nil
		}
		if !errors.Is(err, ErrLocked) || timeout <= 0 || !time.Now().Before(deadline) {
			return nil, err
		}
		remaining := time.Until(deadline)
		if wait > remaining {
			wait = remaining
		}
		time.Sleep(wait)
		if wait < maxWait {
			wait *= 2
			if wait > maxWait {
				wait = maxWait
			}
		}
	}
}

func (l *fileLock) Close() {
	if l.h != 0 {
		syscall.CloseHandle(l.h)
		l.h = 0
	}
}
