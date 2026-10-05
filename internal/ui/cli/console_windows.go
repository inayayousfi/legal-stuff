package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// restoreConsoleInput turns line input, echo, and Ctrl+C handling back on,
// in case an earlier program left the Windows console in raw mode.
func restoreConsoleInput() {
	handle := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return
	}
	windows.SetConsoleMode(handle, mode|windows.ENABLE_PROCESSED_INPUT|windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT)
}
