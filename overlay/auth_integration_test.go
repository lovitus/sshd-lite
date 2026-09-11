//go:build (linux || darwin || windows) && integration

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jpillora/sftp"
	"golang.org/x/crypto/ssh"
)

// This test builds and starts the real CLI, rather than only testing the loader.
// Run with: go test -tags=integration -run TestVirtualAuthEndToEnd -count=1 .
func TestVirtualAuthEndToEnd(t *testing.T) {
	work := t.TempDir()
	binary := os.Getenv("SSHD_LITE_TEST_BINARY")
	if binary == "" {
		binary = filepath.Join(work, "sshd-lite-private")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		build := exec.Command("go", "build", "-o", binary, ".")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build actual CLI: %v\n%s", err, output)
		}
	} else {
		t.Logf("Testing supplied release executable: %s", binary)
	}
	identity, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	shell := "/bin/sh"
	ownerCommand := `id -u; test "${SSHD_LITE_AUTH+x}" != x`
	terminalCommand := `test -t 0 && test -t 1 && test -t 2 && printf '\nPTY_%s\n' OK` + "\n"
	if runtime.GOOS == "windows" {
		shell = "powershell.exe"
		ownerCommand = `[System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value; if (Test-Path Env:SSHD_LITE_AUTH) { exit 3 }`
		terminalCommand = `if (-not [Console]::IsInputRedirected -and -not [Console]::IsOutputRedirected) { Write-Output ('PTY_'+'OK') }` + "\r\n"
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(work, "host_key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"environment-only", "file-only", "file-plus-environment"} {
		useFile := name != "environment-only"
		useEnv := name != "file-only"
		t.Run(name, func(t *testing.T) {
			// Reserve a free loopback port, then release it for the real CLI.
			probe, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := probe.Addr().String()
			_, port, _ := net.SplitHostPort(address)
			probe.Close()
			args := []string{"--host", "127.0.0.1", "--port", port, "--shell", shell, "--workdir", work, "--keyfile", keyPath, "--no-global-env", "--sftp"}
			if backend := os.Getenv("SSHD_LITE_TEST_WINDOWS_PTY"); backend != "" {
				args = append(args, "--windows-pty", backend)
			}
			if useFile {
				p := credentialFile(t, "virtual_alice_only:alice-file-secret\nvirtual_bob_only:bob-old-secret\n")
				args = append(args, "@"+p)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, authEnvironment+"=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			if useEnv {
				cmd.Env = append(cmd.Env, authEnvironment+"=virtual_bob_only:bob-env-secret\nvirtual_ci_only:ci-env-secret")
			}
			logPath := filepath.Join(t.TempDir(), "server.log")
			logFile, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdout, cmd.Stderr = logFile, logFile
			if err := cmd.Start(); err != nil {
				logFile.Close()
				t.Fatal(err)
			}
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = cmd.Wait(); close(done) }()
			t.Cleanup(func() { cancel(); <-done; logFile.Close() })
			ready := false
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
				select {
				case <-done:
					log, _ := os.ReadFile(logPath)
					t.Fatalf("CLI exited before ready: %v\n%s", waitErr, log)
				default:
				}
				c, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
				if err == nil {
					c.Close()
					ready = true
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !ready {
				t.Fatal("CLI did not listen")
			}
			cases := []struct {
				user, password string
				accepted       bool
			}{
				{"virtual_bob_only", "bob-env-secret", useEnv}, {"virtual_ci_only", "ci-env-secret", useEnv},
				{"virtual_alice_only", "alice-file-secret", useFile}, {"virtual_bob_only", "bob-old-secret", useFile && !useEnv},
				{"virtual_bob_only", "ci-env-secret", false}, {"unknown_virtual_name", "bob-env-secret", false},
			}
			for _, tc := range cases {
				client, err := ssh.Dial("tcp", address, &ssh.ClientConfig{
					User: tc.user, Auth: []ssh.AuthMethod{ssh.Password(tc.password)},
					HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 5 * time.Second,
				})
				if !tc.accepted {
					if err == nil {
						client.Close()
						t.Errorf("unexpected authentication success for %s", tc.user)
					}
					continue
				}
				if err != nil {
					t.Fatalf("authentication failed for %s: %v", tc.user, err)
				}
				func() {
					defer client.Close()
					session, err := client.NewSession()
					if err != nil {
						t.Fatal(err)
					}
					output, err := session.CombinedOutput(ownerCommand)
					session.Close()
					if err != nil || strings.TrimSpace(string(output)) != identity.Uid {
						t.Fatalf("owner/env check failed: %v (%q)", err, output)
					}
					statusSession, err := client.NewSession()
					if err != nil {
						t.Fatal(err)
					}
					err = statusSession.Run("exit 7")
					statusSession.Close()
					if status, ok := err.(*ssh.ExitError); !ok || status.ExitStatus() != 7 {
						t.Fatalf("exit status: %v", err)
					}
					files, err := sftp.NewClient(client)
					if err != nil {
						t.Fatal(err)
					}
					remote, err := files.Create("sftp-probe-" + tc.user)
					if err != nil {
						files.Close()
						t.Fatal(err)
					}
					_, err = remote.Write([]byte("SFTP_OK"))
					remote.Close()
					files.Close()
					if err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(filepath.Join(work, "sftp-probe-"+tc.user))
					if err != nil || string(data) != "SFTP_OK" {
						t.Fatalf("SFTP write/workdir check: %v", err)
					}

					ptySession, err := client.NewSession()
					if err != nil {
						t.Fatal(err)
					}
					defer ptySession.Close()
					if err := ptySession.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
						t.Fatal(err)
					}
					stdin, err := ptySession.StdinPipe()
					if err != nil {
						t.Fatal(err)
					}
					stdout, err := ptySession.StdoutPipe()
					if err != nil {
						t.Fatal(err)
					}
					if err := ptySession.Shell(); err != nil {
						t.Fatal(err)
					}
					if _, err := io.WriteString(stdin, terminalCommand); err != nil {
						t.Fatal(err)
					}
					result := make(chan bool, 1)
					go func() {
						scanner := bufio.NewScanner(stdout)
						for scanner.Scan() {
							if strings.TrimSpace(ansi.Strip(scanner.Text())) == "PTY_OK" {
								result <- true
								return
							}
						}
						result <- false
					}()
					select {
					case ok := <-result:
						if !ok {
							t.Fatal("PTY shell ended without terminal marker")
						}
					case <-time.After(20 * time.Second):
						t.Fatal("PTY shell did not confirm terminal descriptors")
					}
					io.WriteString(stdin, "exit\r\n")
					stdin.Close()
					if err := ptySession.Wait(); err != nil {
						t.Fatal(err)
					}
				}()
			}
			cancel()
			<-done
			log, _ := os.ReadFile(logPath)
			for _, password := range []string{"alice-file-secret", "bob-old-secret", "bob-env-secret", "ci-env-secret"} {
				if bytes.Contains(log, []byte(password)) {
					t.Fatal("server logged a password")
				}
			}
			t.Log(fmt.Sprintf("actual CLI verified: %s", name))
		})
	}
}
