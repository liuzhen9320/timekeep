//go:build linux

package sessions

import (
	"os"
	"syscall"
)

// isProcessRunning checks if a process with the given PID is still running on Linux
func isProcessRunning(pid int) bool {
	// On Linux, use syscall.Kill with signal 0 to check if process exists
	// Signal 0 doesn't actually send a signal, just checks if we can send one
	err := syscall.Kill(pid, 0)
	if err == nil {
		// No error means process exists
		return true
	}

	// Check if the error is specifically "no such process"
	if err == syscall.ESRCH {
		// Process doesn't exist
		return false
	}

	// For other errors (like permission denied), assume process exists
	// since we can't definitively say it's gone
	return true
}
