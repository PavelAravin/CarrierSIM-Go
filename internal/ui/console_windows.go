//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableConsole turns on Virtual Terminal Processing so ANSI colors work in Win10 cmd.exe.
func EnableConsole() {
	enableVT(windows.Handle(os.Stdout.Fd()))
	enableVT(windows.Handle(os.Stderr.Fd()))
}

func enableVT(h windows.Handle) {
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
