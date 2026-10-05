//go:build !windows

package cli

// restoreConsoleInput is needed only on Windows.
func restoreConsoleInput() {}
