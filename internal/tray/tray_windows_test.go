//go:build windows

package tray

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procShellNotifyIconGetRect = shell32.NewProc("Shell_NotifyIconGetRect")
var procFindWindowW = user32.NewProc("FindWindowW")
var procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")

const (
	wmKeyDown    = 0x0100
	wmCancelMode = 0x001F
	vkEscape     = 0x1B
)

type notifyIconIdentifier struct {
	cbSize   uint32
	hWnd     windows.HWND
	uID      uint32
	guidItem windows.GUID
}

type rect struct {
	left, top, right, bottom int32
}

func TestWin32StructSizes(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("layout assertions target windows/amd64")
	}
	if got := unsafe.Sizeof(notifyIconData{}); got != 976 {
		t.Fatalf("notifyIconData size = %d, want 976", got)
	}
	if got := unsafe.Sizeof(msgStruct{}); got != 48 {
		t.Fatalf("msgStruct size = %d, want 48", got)
	}
	if got := unsafe.Sizeof(wndClassEx{}); got != 80 {
		t.Fatalf("wndClassEx size = %d, want 80", got)
	}
}

func TestCopyUTF16TruncatesAndTerminates(t *testing.T) {
	var dst [4]uint16
	copyUTF16(dst[:], "abcdef")
	want := [4]uint16{'a', 'b', 'c', 0}
	if dst != want {
		t.Fatalf("copyUTF16 = %v, want %v", dst, want)
	}

	copyUTF16(dst[:], "")
	if dst != [4]uint16{} {
		t.Fatalf("copyUTF16 empty = %v, want all zero", dst)
	}
}

func TestAcquireSingleInstance(t *testing.T) {
	name := fmt.Sprintf(`Local\FatTraceTest-%d`, time.Now().UnixNano())
	release, already, err := AcquireSingleInstance(name)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if already {
		t.Fatal("first acquire reported an existing instance")
	}

	releaseSecond, already, err := AcquireSingleInstance(name)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if !already {
		t.Fatal("second acquire did not detect the first instance")
	}
	releaseSecond()
	release()
}

// TestShellIconIntegration registers a real notification-area icon. It touches
// the live shell, so it only runs when explicitly enabled locally:
//
//	$env:FATTRACE_TRAY_UI_TEST="1"; go test ./internal/tray -run ShellIcon -v
func TestShellIconIntegration(t *testing.T) {
	if os.Getenv("FATTRACE_TRAY_UI_TEST") != "1" {
		t.Skip("set FATTRACE_TRAY_UI_TEST=1 to exercise the live shell")
	}
	tr := New(Options{
		Title:   "FatTrace test",
		Tooltip: "FatTrace test",
		Menu:    func() []Item { return []Item{{ID: 1, Label: "Open"}} },
	})
	done := make(chan error, 1)
	go func() { done <- tr.Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := tr.WaitReady(ctx); err != nil {
		t.Fatalf("tray ready: %v", err)
	}
	t.Cleanup(func() {
		tr.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("tray exit: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("tray did not stop")
		}
	})

	tr.mu.Lock()
	nid := tr.nid
	tr.mu.Unlock()
	ident := notifyIconIdentifier{cbSize: uint32(unsafe.Sizeof(notifyIconIdentifier{})), hWnd: nid.hWnd, uID: nid.uID}
	var r rect
	hr, _, _ := procShellNotifyIconGetRect.Call(
		uintptr(unsafe.Pointer(&ident)), uintptr(unsafe.Pointer(&r)))
	if int32(hr) < 0 {
		t.Fatalf("Shell_NotifyIconGetRect HRESULT=0x%X", uint32(hr))
	}
	if r.right <= r.left || r.bottom <= r.top {
		t.Fatalf("icon rect is empty: %+v", r)
	}
	t.Logf("icon rect: (%d,%d)-(%d,%d)", r.left, r.top, r.right, r.bottom)

	openPopupMenu(t, nid.hWnd)
}

func openPopupMenu(t *testing.T, hwnd windows.HWND) {
	t.Helper()
	class, err := windows.UTF16PtrFromString("#32768")
	if err != nil {
		t.Fatal(err)
	}
	procPostMessageW.Call(uintptr(hwnd), wmTrayCallback, 0, wmRButtonUp)
	menu := waitForOwnMenu(t, class, 2*time.Second)
	if menu == 0 {
		t.Fatal("right-click callback did not open a popup menu")
	}
	t.Logf("popup menu opened: hwnd=%#x", menu)

	procPostMessageW.Call(menu, wmCancelMode, 0, 0)
	if waitForMenuGone(class, 1500*time.Millisecond) {
		return
	}
	procPostMessageW.Call(menu, wmKeyDown, vkEscape, 0)
	if !waitForMenuGone(class, 1500*time.Millisecond) {
		t.Error("popup menu did not close after cancel/escape")
	}
}

func waitForOwnMenu(t *testing.T, class *uint16, timeout time.Duration) uintptr {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		menu, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), 0)
		if menu != 0 {
			var pid uint32
			procGetWindowThreadProcessID.Call(menu, uintptr(unsafe.Pointer(&pid)))
			if pid == uint32(os.Getpid()) {
				return menu
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0
}

func waitForMenuGone(class *uint16, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		menu, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), 0)
		if menu == 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
