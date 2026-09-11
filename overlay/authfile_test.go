package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func credentialFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "users.txt")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	secureTestCredentialFile(t, p)
	return p
}

func TestCredentialRecords(t *testing.T) {
	cases := []struct {
		name, input string
		want        map[string]string
	}{
		{"single", "alice:secret", map[string]string{"alice": "secret"}},
		{"lf", "alice:secret\n", map[string]string{"alice": "secret"}},
		{"crlf", "alice:secret\r\nbob:other\r\n", map[string]string{"alice": "secret", "bob": "other"}},
		{"blank-lines", "\n \t\nalice:secret\n\n", map[string]string{"alice": "secret"}},
		{"password-punctuation", "alice: a:b,;# ! $ ' \\\n", map[string]string{"alice": " a:b,;# ! $ ' \\"}},
		{"case-sensitive", "alice:first\nAlice:second", map[string]string{"alice": "first", "Alice": "second"}},
		{"one-letter-name", "a:/secret", map[string]string{"a": "/secret"}},
		{"no-special-users", "none:pw\nenv:pw\nroot:pw", map[string]string{"none": "pw", "env": "pw", "root": "pw"}},
		{"unicode", "测试:password", map[string]string{"测试": "password"}},
		{"at-limit", "a:" + strings.Repeat("x", maxCredentialBytes-2), map[string]string{"a": strings.Repeat("x", maxCredentialBytes-2)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCredentialRecords(tc.input, "test")
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("unexpected result; err=%v", err)
			}
		})
	}
}

func TestRejectMalformedRecords(t *testing.T) {
	cases := map[string]string{
		"empty": "", "blank": " \n\t\r\n", "missing-colon": "secret-marker", "empty-user": ":secret-marker",
		"empty-password": "alice:", "space-in-name": "a lice:secret-marker", "tab-in-name": "a\tlice:secret-marker",
		"slash": "a/lice:secret-marker", "backslash": "a\\lice:secret-marker", "control": "a\x01lice:secret-marker",
		"unicode-space": "a\u00a0lice:secret-marker", "invalid-utf8": "a\xff:secret-marker",
		"null-password": "alice:secret-marker\x00", "embedded-cr": "alice:secret-marker\rrest",
		"duplicate": "alice:secret-marker\nalice:secret-marker", "second-line": "alice:secret-marker\nbroken-secret-marker",
		"too-large": "alice:" + strings.Repeat("x", maxCredentialBytes),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseCredentialRecords(input, "test")
			if err == nil {
				t.Fatal("expected rejection")
			}
			if strings.Contains(err.Error(), "secret-marker") {
				t.Fatal("error exposed credential contents")
			}
		})
	}
}

func TestSourceMerge(t *testing.T) {
	p := credentialFile(t, "alice:alice-file\nbob:bob-file\n")
	fallback, verify, err := resolveAuthSources("@"+p, "bob:bob-env\nci:ci-env", true)
	if err != nil || fallback != "" || verify == nil {
		t.Fatalf("merge failed: %v", err)
	}
	for _, tc := range []struct {
		user, password string
		want           bool
	}{
		{"alice", "alice-file", true}, {"bob", "bob-env", true}, {"ci", "ci-env", true},
		{"bob", "bob-file", false}, {"alice", "bob-env", false}, {"unknown", "alice-file", false},
		{"Alice", "alice-file", false}, {"alice", "", false},
	} {
		if verify(tc.user, []byte(tc.password)) != tc.want {
			t.Errorf("unexpected auth result for %q", tc.user)
		}
	}
}

func TestFileOnlyAndEnvironmentOnly(t *testing.T) {
	p := credentialFile(t, "alice:secret\nbob:other")
	for _, tc := range []struct {
		name, auth, env string
		present         bool
	}{
		{"file-only", "@" + p, "", false}, {"env-only", "", "alice:secret\nbob:other", true},
		{"CLI-plus-env", "alice:secret", "bob:other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallback, verify, err := resolveAuthSources(tc.auth, tc.env, tc.present)
			if err != nil || fallback != "" || verify == nil || !verify("alice", []byte("secret")) || !verify("bob", []byte("other")) {
				t.Fatalf("bad auth result; err=%v", err)
			}
		})
	}
}

func TestCLIOverride(t *testing.T) {
	_, verify, err := resolveAuthSources("alice:old", "alice:new", true)
	if err != nil || verify == nil || verify("alice", []byte("old")) || !verify("alice", []byte("new")) {
		t.Fatalf("override failed: %v", err)
	}
}

func TestLegacyModesUnchanged(t *testing.T) {
	for _, auth := range []string{"", "none", "alice:secret", "/tmp/authorized_keys", "github.com/alice", `C:\keys\authorized_keys`} {
		got, verify, err := resolveAuthSources(auth, "", false)
		if err != nil || got != auth || verify != nil {
			t.Fatal("upstream mode changed")
		}
	}
}

func TestFailClosed(t *testing.T) {
	good := credentialFile(t, "alice:secret")
	empty := credentialFile(t, "")
	bad := credentialFile(t, "invalid-record")
	for _, tc := range []struct {
		name, auth, env string
		present         bool
	}{
		{"empty-env", "@" + good, "", true}, {"bad-env", "@" + good, "bad-record", true},
		{"duplicate-env", "@" + good, "bob:one\nbob:two", true},
		{"missing-file", "@" + filepath.Join(t.TempDir(), "missing"), "bob:secret", true},
		{"no-path", "@", "bob:secret", true}, {"empty-file", "@" + empty, "bob:secret", true},
		{"bad-file", "@" + bad, "bob:secret", true},
		{"no-auth-conflict", "none", "bob:secret", true},
		{"keyfile-conflict", "/tmp/authorized_keys", "bob:secret", true},
		{"github-conflict", "github.com/alice", "bob:secret", true},
		{"windows-conflict", `C:\keys`, "bob:secret", true},
		{"env-empty-only", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, verify, err := resolveAuthSources(tc.auth, tc.env, tc.present)
			if err == nil || verify != nil {
				t.Fatal("expected fail-closed startup")
			}
		})
	}
}

func TestFilePermissionsAndKinds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows DACLs are tested separately")
	}
	for _, mode := range []os.FileMode{0600, 0400, 0644, 0640, 0604, 0660} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			p := credentialFile(t, "alice:secret")
			if err := os.Chmod(p, mode); err != nil {
				t.Fatal(err)
			}
			_, err := readCredentialFile(p)
			if (err != nil) != (mode&0077 != 0) {
				t.Fatalf("unexpected permissions result: %v", err)
			}
		})
	}
	if _, err := readCredentialFile(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := readCredentialFile(credentialFile(t, "a:"+strings.Repeat("x", maxCredentialBytes))); err == nil {
		t.Fatal("oversized file accepted")
	}
}

func TestUserLimit(t *testing.T) {
	var lines []string
	for i := 0; i < maxCredentialUsers; i++ {
		lines = append(lines, fmt.Sprintf("u%d:p", i))
	}
	if _, err := parseCredentialRecords(strings.Join(lines, "\n"), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCredentialRecords(strings.Join(append(lines, "extra:p"), "\n"), "test"); err == nil {
		t.Fatal("user limit not enforced")
	}
	file := credentialFile(t, strings.Join(lines, "\n"))
	if _, _, err := resolveAuthSources("@"+file, "extra:p", true); err == nil {
		t.Fatal("merged user limit not enforced")
	}
}

func TestEnvironmentIsConsumed(t *testing.T) {
	for _, value := range []string{"bob:secret", "broken"} {
		t.Run(value[:3], func(t *testing.T) {
			t.Setenv(authEnvironment, value)
			_, verify, err := resolveAuthentication("")
			if value == "bob:secret" && (err != nil || verify == nil || !verify("bob", []byte("secret"))) {
				t.Fatal("env authentication failed")
			}
			if value == "broken" && err == nil {
				t.Fatal("invalid env accepted")
			}
			if _, exists := os.LookupEnv(authEnvironment); exists {
				t.Fatal("auth environment still set")
			}
			command := exec.Command("/bin/sh", "-c", `test "${SSHD_LITE_AUTH+x}" != x`)
			if runtime.GOOS == "windows" {
				command = exec.Command("powershell.exe", "-NoProfile", "-Command", `if (Test-Path Env:SSHD_LITE_AUTH) { exit 1 }`)
			}
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("child inherited auth env: %v (%s)", err, out)
			}
		})
	}
}

func TestNoCredentialAddedToArguments(t *testing.T) {
	t.Setenv(authEnvironment, "bob:secret-marker-not-in-args")
	before := append([]string(nil), os.Args...)
	if _, _, err := resolveAuthentication(""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, os.Args) {
		t.Fatal("process arguments were changed")
	}
}

func TestVerifierIsIndependentAndConcurrent(t *testing.T) {
	users := map[string]string{"alice": "secret"}
	verify := newPasswordVerifier(users)
	users["alice"] = "changed"
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if !verify("alice", []byte("secret")) || verify("alice", []byte("changed")) || verify("missing", []byte("secret")) {
					t.Error("incorrect concurrent verification")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func FuzzCredentialRecords(f *testing.F) {
	for _, seed := range []string{"alice:secret", "alice:p\nbob:q", "", "a:p\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		users, err := parseCredentialRecords(value, "fuzz")
		if err != nil {
			return
		}
		verify := newPasswordVerifier(users)
		for user, password := range users {
			if !verify(user, []byte(password)) || verify(user, append([]byte(password), 0)) {
				t.Fatal("verifier mismatch")
			}
			if bytes.ContainsAny([]byte(password), "\r\n\x00") {
				t.Fatal("invalid password accepted")
			}
		}
	})
}
