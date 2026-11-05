//go:build windows

// Package tray implements a minimal Windows notification-area icon with a
// popup menu. It uses only Win32 calls and does not require cgo.
package tray

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo = 0x00000001

	wmTrayCallback  = 0x8000 + 1
	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	mfString       = 0x00000000
	mfGrayed       = 0x00000001
	mfSeparator    = 0x00000800
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW      = user32.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procPostMessageW          = user32.NewProc("PostMessageW")
	procLoadIconW             = user32.NewProc("LoadIconW")
	procCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	procAppendMenuW           = user32.NewProc("AppendMenuW")
	procDestroyMenu           = user32.NewProc("DestroyMenu")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	procShowWindow            = user32.NewProc("ShowWindow")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW          = kernel32.NewProc("CreateMutexW")
	procCloseHandle           = kernel32.NewProc("CloseHandle")
	procShellNotifyIconW      = shell32.NewProc("Shell_NotifyIconW")

	messageLoopCallback = windows.NewCallback(trayWndProc)
)

const className = "FatTraceTrayWindow"

// Item is one popup menu entry. Separator entries ignore the other fields.
type Item struct {
	ID        int
	Label     string
	Disabled  bool
	Separator bool
}

// Options configures the icon and its callbacks. Menu is called on the window
// thread every time the menu opens; callbacks are dispatched on their own
// goroutine and must not block.
type Options struct {
	Title     string
	Tooltip   string
	Menu      func() []Item
	OnOpen    func()
	OnCommand func(int)
}

// Tray owns one notification-area icon.
type Tray struct {
	opts Options

	ready chan struct{}
	done  chan struct{}

	mu      sync.Mutex
	hwnd    windows.HWND
	nid     notifyIconData
	icon    windows.Handle
	runErr  error
	removed bool
}

// New creates a tray handle. Call Run to create the icon and start the window
// message loop; Run must be called from a dedicated goroutine.
func New(opts Options) *Tray {
	if opts.Title == "" {
		opts.Title = "FatTrace"
	}
	return &Tray{
		opts:  opts,
		ready: make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// Run registers the icon and blocks until Stop is called or the message loop
// ends. It locks its OS thread because the window belongs to it.
func (t *Tray) Run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	err := t.run()
	t.mu.Lock()
	t.runErr = err
	t.mu.Unlock()
	close(t.done)
	return err
}

func (t *Tray) run() error {
	instance, _, callErr := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return fmt.Errorf("tray: module handle: %w", callErr)
	}
	class, err := windows.UTF16PtrFromString(className)
	if err != nil {
		return err
	}
	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   messageLoopCallback,
		hInstance:     windows.Handle(instance),
		lpszClassName: class,
	}
	atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 && !errors.Is(callErr, windows.ERROR_CLASS_ALREADY_EXISTS) {
		return fmt.Errorf("tray: register class: %w", callErr)
	}

	title, err := windows.UTF16PtrFromString(t.opts.Title)
	if err != nil {
		return err
	}
	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0,
		uintptr(windows.HWND(^uintptr(2))), // HWND_MESSAGE
		0,
		uintptr(instance),
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("tray: create window: %w", callErr)
	}
	t.mu.Lock()
	t.hwnd = windows.HWND(hwnd)
	t.mu.Unlock()
	registerWindow(t.hwnd, t)

	icon, _, _ := procLoadIconW.Call(0, 32512) // IDI_APPLICATION
	t.mu.Lock()
	t.icon = windows.Handle(icon)
	t.nid = notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             t.hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayCallback,
		hIcon:            t.icon,
	}
	copyUTF16(t.nid.szTip[:], t.opts.Tooltip)
	nid := t.nid
	t.mu.Unlock()
	if ok, _, callErr := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); ok == 0 {
		procDestroyWindow.Call(hwnd)
		return fmt.Errorf("tray: add icon: %w", callErr)
	}
	close(t.ready)

	var msg msgStruct
	for {
		ret, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) == -1 {
			return fmt.Errorf("tray: message loop: %w", callErr)
		}
		if ret == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// Ready is closed once the icon is visible.
func (t *Tray) Ready() <-chan struct{} { return t.ready }

// WaitReady waits for the icon or returns the startup error.
func (t *Tray) WaitReady(ctx context.Context) error {
	select {
	case <-t.ready:
		return nil
	case <-t.done:
		t.mu.Lock()
		err := t.runErr
		t.mu.Unlock()
		if err == nil {
			err = errors.New("tray: stopped before becoming ready")
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done is closed when the message loop exits.
func (t *Tray) Done() <-chan struct{} { return t.done }

// Stop asks the window to close; Run then removes the icon and returns.
func (t *Tray) Stop() {
	t.mu.Lock()
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(uintptr(hwnd), wmClose, 0, 0)
	}
}

// SetTooltip replaces the hover text. It is safe to call before Run, in which
// case the value becomes the initial tooltip if Options.Tooltip was empty.
func (t *Tray) SetTooltip(text string) {
	t.mu.Lock()
	t.opts.Tooltip = text
	if t.hwnd == 0 || t.removed {
		t.mu.Unlock()
		return
	}
	nid := t.nid
	copyUTF16(nid.szTip[:], text)
	t.mu.Unlock()
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// Notify shows a balloon notification for a few seconds.
func (t *Tray) Notify(title, text string) {
	t.mu.Lock()
	if t.hwnd == 0 || t.removed {
		t.mu.Unlock()
		return
	}
	nid := t.nid
	nid.uFlags |= nifInfo
	nid.dwInfoFlags = niifInfo
	nid.uTimeout = 10000
	copyUTF16(nid.szInfoTitle[:], title)
	copyUTF16(nid.szInfo[:], text)
	t.mu.Unlock()
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (t *Tray) removeIcon() {
	t.mu.Lock()
	if t.removed {
		t.mu.Unlock()
		return
	}
	t.removed = true
	nid := t.nid
	t.mu.Unlock()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (t *Tray) showMenu() {
	if t.opts.Menu == nil {
		return
	}
	items := t.opts.Menu()
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	for _, item := range items {
		if item.Separator {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		if item.Disabled {
			flags |= mfGrayed
		}
		label, err := windows.UTF16PtrFromString(item.Label)
		if err != nil {
			continue
		}
		procAppendMenuW.Call(menu, flags, uintptr(item.ID), uintptr(unsafe.Pointer(label)))
	}

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	t.mu.Lock()
	hwnd := t.hwnd
	t.mu.Unlock()
	procSetForegroundWindow.Call(uintptr(hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(menu,
		tpmReturnCmd|tpmRightButton,
		uintptr(pt.x), uintptr(pt.y), 0, uintptr(hwnd), 0)
	// Required so the menu closes when the user clicks elsewhere.
	procPostMessageW.Call(uintptr(hwnd), wmNull, 0, 0)
	if cmd != 0 && t.opts.OnCommand != nil {
		go t.opts.OnCommand(int(cmd))
	}
}

var (
	windowsMu  sync.Mutex
	trayByHWND = map[windows.HWND]*Tray{}
)

func registerWindow(hwnd windows.HWND, t *Tray) {
	windowsMu.Lock()
	trayByHWND[hwnd] = t
	windowsMu.Unlock()
}

func lookupWindow(hwnd uintptr) *Tray {
	windowsMu.Lock()
	defer windowsMu.Unlock()
	return trayByHWND[windows.HWND(hwnd)]
}

func unregisterWindow(hwnd windows.HWND) {
	windowsMu.Lock()
	delete(trayByHWND, hwnd)
	windowsMu.Unlock()
}

func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmTrayCallback:
		t := lookupWindow(hwnd)
		if t == nil {
			return 0
		}
		switch lparam & 0xffff {
		case wmLButtonDblClk:
			if t.opts.OnOpen != nil {
				go t.opts.OnOpen()
			}
		case wmRButtonUp:
			t.showMenu()
		}
		return 0
	case wmCommand:
		t := lookupWindow(hwnd)
		if t != nil && t.opts.OnCommand != nil {
			if id := int(wparam & 0xffff); id > 0 {
				go t.opts.OnCommand(id)
			}
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if t := lookupWindow(hwnd); t != nil {
			t.removeIcon()
			unregisterWindow(windows.HWND(hwnd))
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return ret
}

// HideOwnConsole hides the console only when this process is the sole owner
// (double-click launch). An inherited terminal is left untouched.
func HideOwnConsole() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), 2)
	if n == 1 {
		const swHide = 0
		procShowWindow.Call(hwnd, swHide)
	}
}

// ErrorBox shows a blocking error dialog. Used by the tray launcher because its
// console may be hidden.
func ErrorBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	const mbOK, mbIconError = 0x0, 0x10
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbOK|mbIconError)
}

// AcquireSingleInstance creates a named mutex. alreadyRunning is true when
// another process already owns the name; release must still be called when it
// is false.
func AcquireSingleInstance(name string) (release func(), alreadyRunning bool, err error) {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if h == 0 {
		return nil, false, callErr
	}
	if errors.Is(callErr, windows.ERROR_ALREADY_EXISTS) {
		procCloseHandle.Call(h)
		return func() {}, true, nil
	}
	return func() { procCloseHandle.Call(h) }, false, nil
}

func copyUTF16(dst []uint16, s string) {
	if len(dst) == 0 {
		return
	}
	for i := range dst {
		dst[i] = 0
	}
	units, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	copy(dst[:len(dst)-1], units)
}

// ---------- Win32 structures ----------

type point struct {
	x, y int32
}

type msgStruct struct {
	hwnd    windows.HWND
	message uint32
	_       uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             windows.HWND
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            windows.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uTimeout         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     windows.Handle
}
