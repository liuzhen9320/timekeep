//go:build windows

package sessions

import "golang.org/x/sys/windows"

// isProcessRunning checks if a process with the given PID is still running on Windows
func isProcessRunning(pid int) bool {
	// On Windows, use OpenProcess to check if the process exists
	// This is more reliable than Signal(0)
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		// Process doesn't exist or can't be opened
		return false
	}
	defer windows.CloseHandle(handle)

	// Check if process has exited using GetExitCodeProcess
	var exitCode uint32
	err = windows.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		// Error getting exit code means process is gone
		return false
	}

	// If exit code is STILL_ACTIVE (259), process is still running
	// Otherwise it has exited
	return exitCode == 259
}
