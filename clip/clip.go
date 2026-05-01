// Package clip provides clipboard read/write and Ctrl+V paste simulation (Windows).
package clip

import (
	"fmt"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procOpenClipboard   = user32.NewProc("OpenClipboard")
	procCloseClipboard  = user32.NewProc("CloseClipboard")
	procEmptyClipboard  = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procKeybdEvent      = user32.NewProc("keybd_event")

	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGlobalAlloc = kernel32.NewProc("GlobalAlloc")
	procGlobalLock  = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalFree  = kernel32.NewProc("GlobalFree")
)

const (
	CF_UNICODETEXT = 13
	GMEM_MOVABLE   = 0x0002
	GMEM_ZEROINIT  = 0x0040

	VK_CONTROL     = 0x11
	VK_V           = 0x56
	KEYEVENTF_KEYUP = 0x0002
)

// SetText copies text to system clipboard (CF_UNICODETEXT).
func SetText(text string) error {
	ret, _, _ := procOpenClipboard.Call(0)
	if ret == 0 {
		return fmt.Errorf("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()

	// UTF-16LE null-terminated
	u16 := utf16.Encode([]rune(text + "\x00"))
	size := len(u16) * 2

	hMem, _, _ := procGlobalAlloc.Call(GMEM_MOVABLE, uintptr(size))
	if hMem == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}

	locked, _, _ := procGlobalLock.Call(hMem)
	if locked == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("GlobalLock failed")
	}

	buf := (*[1 << 20]uint16)(unsafe.Pointer(locked))[:len(u16)]
	copy(buf, u16)

	procGlobalUnlock.Call(hMem)

	// hMem ownership transferred to clipboard on success
	ret, _, _ = procSetClipboardData.Call(CF_UNICODETEXT, hMem)
	if ret == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("SetClipboardData failed")
	}

	return nil
}

// GetText reads text from system clipboard (CF_UNICODETEXT).
// Returns empty string if clipboard is empty or not text.
func GetText() (string, error) {
	ret, _, _ := procOpenClipboard.Call(0)
	if ret == 0 {
		return "", fmt.Errorf("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()

	hMem, _, _ := procGetClipboardData.Call(CF_UNICODETEXT)
	if hMem == 0 {
		return "", nil
	}

	locked, _, _ := procGlobalLock.Call(hMem)
	if locked == 0 {
		return "", nil
	}
	defer procGlobalUnlock.Call(hMem)

	p := (*[1 << 20]uint16)(unsafe.Pointer(locked))
	length := 0
	for p[length] != 0 {
		length++
	}

	u16 := make([]uint16, length)
	copy(u16, p[:length])

	return string(utf16.Decode(u16)), nil
}

// SendCtrlV simulates Ctrl+V keystroke in the active foreground window.
func SendCtrlV() {
	// Ctrl down, V down, V up, Ctrl up
	procKeybdEvent.Call(VK_CONTROL, 0, 0, 0)
	procKeybdEvent.Call(VK_V, 0, 0, 0)
	procKeybdEvent.Call(VK_V, 0, KEYEVENTF_KEYUP, 0)
	procKeybdEvent.Call(VK_CONTROL, 0, KEYEVENTF_KEYUP, 0)
}
