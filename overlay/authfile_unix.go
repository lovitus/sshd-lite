//go:build !windows

package main

import (
	"fmt"
	"os"
)

func checkCredentialFileMode(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("credential file must be a regular file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("credential file must not grant group/other permissions; use chmod 600")
	}
	return nil
}

func checkCredentialFileAccess(f *os.File) error { return nil }
