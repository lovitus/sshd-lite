package legacypty

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExactEnvironment(t *testing.T) {
	t.Setenv("LEGACYPT_TEST_SECRET", "must-not-inherit")
	block, err := environmentBlock([]string{"A=1", "a=2", "EMPTY="})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, r := range block {
		text.WriteRune(rune(r))
	}
	if strings.Contains(text.String(), "must-not-inherit") || strings.Contains(text.String(), "A=1") || !strings.Contains(text.String(), "a=2") || !strings.Contains(text.String(), "EMPTY=") {
		t.Fatal("environment was not preserved exactly")
	}
}

func TestNativeWinPTY(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("embedded legacy DLL is x64")
	}
	defer Cleanup()
	command := exec.Command("cmd.exe", "/Q", "/D")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "LEGACYPT_VALUE=exact-value")
	terminal, err := Start(command, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	var out bytes.Buffer
	outputDone := make(chan struct{})
	go func() { io.Copy(&out, terminal); close(outputDone) }()
	// Input is deliberately sent immediately after spawning, without sleeps.
	if _, err := terminal.Write([]byte("echo LEGACY_READY\r\necho %LEGACYPT_VALUE%\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := terminal.Resize(100, 35); err != nil {
		t.Fatal(err)
	}
	terminal.Write([]byte("exit 7\r\n"))
	waited := make(chan struct{})
	var state *os.ProcessState
	go func() { state, err = command.Process.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(15 * time.Second):
		command.Process.Kill()
		<-waited
		t.Fatal("legacy shell hung")
	}
	// AUTO_SHUTDOWN drains the console and closes its output pipe.
	select {
	case <-outputDone:
	case <-time.After(5 * time.Second):
		terminal.Close()
		<-outputDone
		t.Fatal("legacy output did not close")
	}
	if err != nil || state.ExitCode() != 7 || !strings.Contains(out.String(), "LEGACY_READY") || !strings.Contains(out.String(), "exact-value") {
		t.Fatalf("legacy result: %v %v %q", state, err, out.String())
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); terminal.Resize(80, 24); terminal.Close() }()
	}
	wg.Wait()
	backend.Lock()
	dir := backend.dir
	backend.Unlock()
	if !filepath.IsAbs(dir) {
		t.Fatal("dependency path is not absolute")
	}
	for _, name := range []string{"winpty.dll", "winpty-agent.exe"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dependency directory not cleaned: %v", err)
	}
}
