//go:build !windows

package terminal

import "fmt"

func ConfigureWindowsBackend(mode string) error {
	if mode != "" && mode != "auto" {
		return fmt.Errorf("--windows-pty is only available on Windows")
	}
	return nil
}
func CloseWindowsBackend() error { return nil }
