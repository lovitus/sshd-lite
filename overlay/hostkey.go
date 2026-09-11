package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/jpillora/sshd-lite/sshd"
	"github.com/jpillora/sshd-lite/sshd/key"
	"golang.org/x/crypto/ssh"
)

// Persistence is a CLI default; library callers retain upstream's ephemeral default.
func configureHostKey(c *sshd.Config) error {
	if c.KeyFile != "" || c.KeySeed != "" || len(c.KeyBytes) != 0 {
		return nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("locate host key directory (or specify --keyfile): %w", err)
	}
	path := filepath.Join(dir, "sshd-lite", "host_key")
	b, err := loadOrCreateHostKey(path)
	if err != nil {
		return fmt.Errorf("persistent host key %s (or specify --keyfile): %w", path, err)
	}
	c.KeyBytes = b
	if !c.LogQuiet {
		log.Printf("Persistent host key: %s", path)
	}
	return nil
}

func readHostKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("host key must be a regular file, not a symlink or directory")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("host key changed while opening")
	}
	if err := checkCredentialFileMode(opened); err != nil {
		return nil, err
	}
	if err := checkCredentialFileAccess(f); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 64*1024 {
		return nil, fmt.Errorf("host key exceeds 64 KiB")
	}
	if _, err := ssh.ParsePrivateKey(b); err != nil {
		return nil, fmt.Errorf("invalid host key: %w", err)
	}
	return b, nil
}

func loadOrCreateHostKey(path string) ([]byte, error) {
	b, err := readHostKey(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return b, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Publish only a complete, synced, access-restricted key. Hard-link creation
	// is atomic and cannot replace a key another startup has already published.
	f, err := createHostKeyTemp(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	b, err = key.GenerateKey("", true)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(b); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(f.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return readHostKey(path)
}
