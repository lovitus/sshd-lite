package terminal

import (
	"errors"
	"fmt"
	"github.com/jpillora/sshd-lite/internal/legacypty"
	"golang.org/x/sys/windows"
)

var windowsBackend = "auto" // Configured once by the CLI before serving.
func ConfigureWindowsBackend(mode string) error {
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "conpty", "winpty":
	default:
		return fmt.Errorf("invalid --windows-pty %q", mode)
	}
	windowsBackend = mode
	return nil
}
func CloseWindowsBackend() error { return legacypty.Cleanup() }
func findConPTYProcedure(name string) error {
	return windows.NewLazySystemDLL("kernel32.dll").NewProc(name).Find()
}

// Only a missing entry point permits automatic fallback, never arbitrary
// loading, spawning, access-denied or working-directory errors.
func selectLegacyBackend(mode string, find func(string) error) (bool, error) {
	if mode == "winpty" {
		return true, nil
	}
	for _, name := range []string{"CreatePseudoConsole", "ResizePseudoConsole", "ClosePseudoConsole"} {
		if err := find(name); err != nil {
			if mode == "auto" && errors.Is(err, windows.ERROR_PROC_NOT_FOUND) {
				return true, nil
			}
			return false, fmt.Errorf("ConPTY %s: %w", name, err)
		}
	}
	return false, nil
}
