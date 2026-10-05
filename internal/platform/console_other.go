//go:build !windows

package platform

// RestoreConsoleInput is needed only on Windows.
func RestoreConsoleInput() {}
