//go:build !windows

package main

import "os"

func createHostKeyTemp(dir string) (*os.File, error) { return os.CreateTemp(dir, ".host-key-*") }
