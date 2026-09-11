//go:build (linux || darwin || windows) && integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func compatBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("SSHD_LITE_TEST_BINARY"); binary != "" {
		t.Logf("Testing supplied release executable: %s", binary)
		return binary
	}
	binary := filepath.Join(t.TempDir(), "sshd-lite")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
}

// Uses real SSH handshakes across separate CLI processes and working directories.
func TestCompatibilityEndToEnd(t *testing.T) {
	binary := compatBinary(t)
	state := t.TempDir()
	start := func(t *testing.T, shell string, extra ...string) (*ssh.Client, func()) {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		_, port, _ := net.SplitHostPort(address)
		listener.Close()
		if shell == "" {
			shell = "/bin/sh"
			if runtime.GOOS == "windows" {
				shell = "powershell.exe"
			}
		}
		args := append([]string{"--host", "127.0.0.1", "--port", port, "--shell", shell, "--no-global-env"}, extra...)
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = t.TempDir()
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			switch strings.ToUpper(name) {
			case "SSHD_LITE_AUTH", "HOME", "XDG_CONFIG_HOME", "APPDATA":
				continue
			}
			cmd.Env = append(cmd.Env, entry)
		}
		cmd.Env = append(cmd.Env, "HOME="+state, "XDG_CONFIG_HOME="+state, "APPDATA="+state, "SSHD_LITE_AUTH=compat:compat-test-password")
		logs, err := os.Create(filepath.Join(t.TempDir(), "server.log"))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = logs, logs
		if err := cmd.Start(); err != nil {
			cancel()
			logs.Close()
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				cancel()
				<-done
				logs.Close()
			}
		}
		t.Cleanup(stop)
		var client *ssh.Client
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
			client, err = ssh.Dial("tcp", address, &ssh.ClientConfig{User: "compat", Auth: []ssh.AuthMethod{ssh.Password("compat-test-password")}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
			if err == nil {
				return client, stop
			}
			select {
			case <-done:
				b, _ := os.ReadFile(logs.Name())
				t.Fatalf("daemon exited: %s", b)
			default:
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("connect: %v", err)
		return nil, stop
	}
	fingerprint := func(client *ssh.Client) string {
		// The handshake callback below obtains the key through a second connection.
		var result string
		c, err := ssh.Dial("tcp", client.RemoteAddr().String(), &ssh.ClientConfig{User: "compat", Auth: []ssh.AuthMethod{ssh.Password("compat-test-password")}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error { result = ssh.FingerprintSHA256(key); return nil }, Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
		return result
	}
	first, stop := start(t, "")
	before := fingerprint(first)
	first.Close()
	stop()
	second, stop := start(t, "")
	after := fingerprint(second)
	second.Close()
	stop()
	if before != after {
		t.Fatalf("restart changed fingerprint: %s -> %s", before, after)
	}
	t.Log("Actual CLI restart and working-directory change preserved host fingerprint")

	for _, shell := range []string{"default", "cmd"} {
		if shell == "cmd" && runtime.GOOS != "windows" {
			continue
		}
		t.Run("pipe-shell-"+shell, func(t *testing.T) {
			shellPath := ""
			if shell == "cmd" {
				shellPath = "cmd.exe"
			}
			client, stop := start(t, shellPath, "--no-pty")
			defer stop()
			defer client.Close()
			session, err := client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
				t.Fatal("pipe mode falsely accepted a PTY")
			}
			var stdout, stderr bytes.Buffer
			session.Stdout, session.Stderr = &stdout, &stderr
			stdin, err := session.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := session.Shell(); err != nil {
				t.Fatal(err)
			}
			command := "printf 'PIPE_%s\\n' OK\nexit 7\n"
			if runtime.GOOS == "windows" {
				command = "Write-Output ('PIPE_'+'OK')\r\nexit 7\r\n"
			}
			if shell == "cmd" {
				command = "echo PIPE_OK\r\nexit 7\r\n"
			}
			if _, err := io.WriteString(stdin, command); err != nil {
				t.Fatal(err)
			}
			stdin.Close()
			done := make(chan error, 1)
			go func() { done <- session.Wait() }()
			select {
			case err := <-done:
				status, ok := err.(*ssh.ExitError)
				if !ok || status.ExitStatus() != 7 {
					t.Fatalf("pipe exit: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
				if !strings.Contains(stdout.String(), "PIPE_OK") {
					t.Fatalf("pipe output: %q (%q)", stdout.String(), stderr.String())
				}
			case <-time.After(15 * time.Second):
				client.Close()
				<-done
				t.Fatal("pipe shell hung")
			}
			t.Log(fmt.Sprintf("Actual executable basic shell verified: %s (PTY rejected, stdin EOF, output and exit status)", shell))
		})
	}
}
