package main

import "testing"

func TestWorkdirShortFlagRemainsStable(t *testing.T) {
	for _, args := range [][]string{
		{"sshd-lite", "-s", "-t", "--keepalive", "60", "-w", `C:\Users\Administrator`, "--mosh", "test-user:test-password", "-p", "2222"},
		{"sshd-lite", "--windows-pty", "winpty", "-w", `C:\Users\Administrator`, "test-user:test-password"},
	} {
		cli, server, _ := newCLI()
		if _, err := cli.ParseArgsError(normalizeCLIArgs(args)); err != nil {
			t.Fatal(err)
		}
		if server.WorkDir != `C:\Users\Administrator` {
			t.Fatalf("-w changed meaning: workdir=%q", server.WorkDir)
		}
		if server.WindowsPTY != "" && server.WindowsPTY != "winpty" {
			t.Fatalf("unexpected backend %q", server.WindowsPTY)
		}
	}
}
