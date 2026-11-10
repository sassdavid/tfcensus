//go:build windows

package app

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// EnableVirtualTerminal turns on ANSI escape handling when w is a Windows console. It
// returns false only for a console that refuses, where colors would print as raw codes.
func EnableVirtualTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return true
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return true // a pipe, file or mintty, none of which need console modes
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
