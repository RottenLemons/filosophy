// Copyright (C) 2025 Filosophy
//
// Custom Windows system tray implementation.
// Replaces getlantern/systray in the main process to fix:
//   - right-click context menu only working once (missing SetForegroundWindow + WM_NULL)
//   - no double-click support (getlantern/systray doesn't expose WM_LBUTTONDBLCLK)
//
// The tray goroutine is locked to a dedicated OS thread so the Win32 message
// pump is stable. Status updates are driven by App.trayStatusCh.

//go:build windows

package main

import (
	"log"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ── Win32 API procs ───────────────────────────────────────────────────────────

var (
	modUser32  = windows.NewLazySystemDLL("user32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")

	procAppendMenuW         = modUser32.NewProc("AppendMenuW")
	procCreatePopupMenu     = modUser32.NewProc("CreatePopupMenu")
	procCreateWindowExW     = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW      = modUser32.NewProc("DefWindowProcW")
	procDestroyMenu         = modUser32.NewProc("DestroyMenu")
	procDispatchMessageW    = modUser32.NewProc("DispatchMessageW")
	procGetCursorPos        = modUser32.NewProc("GetCursorPos")
	procGetMessageW         = modUser32.NewProc("GetMessageW")
	procLoadImageW          = modUser32.NewProc("LoadImageW")
	procPostMessageW        = modUser32.NewProc("PostMessageW")
	procPostQuitMessage     = modUser32.NewProc("PostQuitMessage")
	procRegisterClassExW    = modUser32.NewProc("RegisterClassExW")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procTrackPopupMenu      = modUser32.NewProc("TrackPopupMenu")
	procTranslateMessage    = modUser32.NewProc("TranslateMessage")

	procShellNotifyIconW = modShell32.NewProc("Shell_NotifyIconW")
)

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	// Custom window messages (WM_USER range)
	wmTrayNotify = 0x0401 // sent by Shell_NotifyIcon as the callback message
	wmUpdateTip  = 0x0402 // posted internally to update the tooltip from another thread

	// Shell_NotifyIcon dwMessage values
	nimAdd    uint32 = 0
	nimModify uint32 = 1
	nimDelete uint32 = 2

	// NOTIFYICONDATA uFlags
	nifMessage uint32 = 0x01
	nifIcon    uint32 = 0x02
	nifTip     uint32 = 0x04

	// Tray callback lParam values
	wmLButtonDblClk uintptr = 0x0203
	wmRButtonUp     uintptr = 0x0205

	// Window messages
	wmCommand uint32 = 0x0111
	wmDestroy uint32 = 0x0002
	wmNull    uint32 = 0x0000

	// TrackPopupMenu flags
	tpmReturnCmd  uint32 = 0x0100
	tpmBottomAlign uint32 = 0x0020

	// AppendMenu flags
	mfString    uint32 = 0x0000
	mfSeparator uint32 = 0x0800

	// Menu command IDs
	cmdShow uint32 = 1001
	cmdQuit uint32 = 1002

	// LoadImage constants
	imageIcon      uint32 = 1
	lrLoadFromFile uint32 = 0x00000010
	lrDefaultSize  uint32 = 0x00000040

	// HWND_MESSAGE creates a message-only window (no Z-order, not visible)
	hwndMessage = ^uintptr(2) // (HWND)(LONG_PTR)(-3)
)

// ── Structures ────────────────────────────────────────────────────────────────
// Field layout verified against the Win32 NOTIFYICONDATAW on 64-bit Windows.

type notifyIconDataW struct {
	cbSize           uint32
	_pad0            [4]byte   // align hWnd to 8 bytes
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	_pad1            [4]byte   // align hIcon to 8 bytes
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type msgW struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      [2]int32
}

type pointW struct{ x, y int32 }

// ── Package-level tray state ──────────────────────────────────────────────────

var (
	trayHwnd    uintptr       // set once the message window is created
	trayNID     notifyIconDataW
	trayTip     atomic.Value  // stores string; read in wmUpdateTip handler
	trayAppRef  *App          // set in runTray; used inside wndProc
	wndProcCB   uintptr       // NewCallback result; kept alive to prevent GC
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func u16ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func copyU16(dst []uint16, s string) {
	src := syscall.StringToUTF16(s)
	copy(dst, src)
}

func shellNotify(msg uint32, nid *notifyIconDataW) bool {
	r, _, _ := procShellNotifyIconW.Call(uintptr(msg), uintptr(unsafe.Pointer(nid)))
	return r != 0
}

// loadTrayIcon writes the embedded ICO bytes to a temp file, loads a HICON,
// then removes the temp file. Returns 0 on failure (tray will be icon-less).
func loadTrayIcon() uintptr {
	f, err := os.CreateTemp("", "filosophy-*.ico")
	if err != nil {
		log.Printf("tray: create temp icon: %v", err)
		return 0
	}
	path := f.Name()
	if _, err := f.Write(trayIcon); err != nil {
		f.Close()
		os.Remove(path)
		log.Printf("tray: write icon: %v", err)
		return 0
	}
	f.Close()
	h, _, _ := procLoadImageW.Call(
		0,
		uintptr(unsafe.Pointer(u16ptr(path))),
		uintptr(imageIcon),
		0, 0,
		uintptr(lrLoadFromFile|lrDefaultSize),
	)
	os.Remove(path)
	return h
}

func setTrayTip(tip string) {
	copyU16(trayNID.szTip[:], tip)
	shellNotify(nimModify, &trayNID)
}

// showContextMenu is called on WM_RBUTTONUP. The two critical Win32 rules that
// getlantern/systray misses — causing right-click to stop working after the
// first use — are:
//   1. SetForegroundWindow BEFORE TrackPopupMenu
//   2. PostMessage WM_NULL AFTER TrackPopupMenu
func showContextMenu(hwnd uintptr) {
	var pt pointW
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	procAppendMenuW.Call(hMenu, uintptr(mfString), uintptr(cmdShow),
		uintptr(unsafe.Pointer(u16ptr("Show Filosophy"))))
	procAppendMenuW.Call(hMenu, uintptr(mfSeparator), 0, 0)
	procAppendMenuW.Call(hMenu, uintptr(mfString), uintptr(cmdQuit),
		uintptr(unsafe.Pointer(u16ptr("Quit"))))

	// Rule 1: give our window foreground focus so the menu gets proper keyboard/mouse routing.
	procSetForegroundWindow.Call(hwnd)

	cmd, _, _ := procTrackPopupMenu.Call(
		hMenu,
		uintptr(tpmReturnCmd|tpmBottomAlign),
		uintptr(pt.x),
		uintptr(pt.y),
		0,
		hwnd,
		0,
	)

	// Rule 2: reset the tray message pump so the next right-click is delivered.
	procPostMessageW.Call(hwnd, uintptr(wmNull), 0, 0)

	switch uint32(cmd) {
	case cmdShow:
		showMainWindow()
	case cmdQuit:
		quitAll(hwnd)
	}
}

func showMainWindow() {
	if trayAppRef != nil && trayAppRef.ctx != nil {
		wailsruntime.WindowShow(trayAppRef.ctx)
	}
}

func quitAll(hwnd uintptr) {
	shellNotify(nimDelete, &trayNID)
	procPostQuitMessage.Call(0)
	if trayAppRef != nil && trayAppRef.ctx != nil {
		wailsruntime.Quit(trayAppRef.ctx)
	}
}

// ── Window procedure ──────────────────────────────────────────────────────────

func trayWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch uint32(msg) {
	case wmTrayNotify:
		switch lParam {
		case wmLButtonDblClk:
			showMainWindow()
		case wmRButtonUp:
			showContextMenu(hwnd)
		}
		return 0

	case wmUpdateTip:
		if tip, ok := trayTip.Load().(string); ok && tip != "" {
			setTrayTip(tip)
		}
		return 0

	case wmDestroy:
		shellNotify(nimDelete, &trayNID)
		procPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

// ── Entry point ───────────────────────────────────────────────────────────────

// runTray creates the tray icon and runs the Win32 message loop.
// Must be called from a goroutine that is locked to an OS thread.
// Blocks until the app quits.
func runTray(app *App) {
	runtime.LockOSThread()
	trayAppRef = app

	hIcon := loadTrayIcon()

	className := u16ptr("FilosophyTrayWnd")
	wndProcCB = syscall.NewCallback(trayWndProc)

	wc := wndClassExW{
		lpfnWndProc:   wndProcCB,
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	// Message-only window: invisible, no Z-order entry, just a message sink.
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(u16ptr("Filosophy"))),
		0,
		0, 0, 0, 0,
		hwndMessage, // parent = HWND_MESSAGE
		0, 0, 0,
	)
	if hwnd == 0 {
		log.Println("tray: CreateWindowExW failed")
		return
	}
	trayHwnd = hwnd

	// Register the notification area icon.
	trayNID = notifyIconDataW{
		hWnd:             hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayNotify,
		hIcon:            hIcon,
	}
	trayNID.cbSize = uint32(unsafe.Sizeof(trayNID))
	copyU16(trayNID.szTip[:], "Filosophy")
	if !shellNotify(nimAdd, &trayNID) {
		log.Println("tray: Shell_NotifyIcon NIM_ADD failed")
	}

	// Forward status strings from App.trayStatusCh to the message window.
	// PostMessage is thread-safe, so this goroutine can post from anywhere.
	go func() {
		for label := range app.trayStatusCh {
			trayTip.Store(label)
			if trayHwnd != 0 {
				procPostMessageW.Call(trayHwnd, uintptr(wmUpdateTip), 0, 0)
			}
		}
	}()

	// Win32 message loop — runs on this OS-locked thread.
	var m msgW
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) { // WM_QUIT or error
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	shellNotify(nimDelete, &trayNID)
	trayHwnd = 0
}
