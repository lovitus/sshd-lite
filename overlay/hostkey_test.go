package main

import (
	"bytes"
	"github.com/jpillora/sshd-lite/sshd"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPersistentHostKeyConcurrentStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "host_key")
	const count = 12
	keys := make([][]byte, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) { defer wg.Done(); keys[i], errs[i] = loadOrCreateHostKey(path) }(i)
	}
	wg.Wait()
	for i := range keys {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if !bytes.Equal(keys[0], keys[i]) {
			t.Fatal("concurrent startups got different identities")
		}
	}
	b, err := loadOrCreateHostKey(path)
	if err != nil || !bytes.Equal(b, keys[0]) {
		t.Fatalf("restart changed host key: %v", err)
	}
}

func TestPersistentHostKeyDoesNotReplaceInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_key")
	if _, err := loadOrCreateHostKey(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateHostKey(path); err == nil {
		t.Fatal("invalid key accepted or replaced")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "broken key" {
		t.Fatal("existing key overwritten")
	}
}

func TestExplicitHostIdentityUnchanged(t *testing.T) {
	for _, c := range []sshd.Config{{KeyFile: "explicit-key"}, {KeySeed: "explicit-seed"}, {KeyBytes: []byte("explicit-bytes")}} {
		original := c
		if err := configureHostKey(&c); err != nil {
			t.Fatal(err)
		}
		if c.KeyFile != original.KeyFile || c.KeySeed != original.KeySeed || !bytes.Equal(c.KeyBytes, original.KeyBytes) {
			t.Fatal("explicit identity overridden")
		}
	}
}
