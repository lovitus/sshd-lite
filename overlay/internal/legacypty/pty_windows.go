// Package legacypty adapts the native WinPTY 0.4.3 C API for sshd-lite.
// It owns only the terminal and agent; the caller owns the child process wait.
package legacypty

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

type api struct{ p map[string]*windows.Proc }

func newAPI() (*api, error) {
	dll, err := load()
	if err != nil {
		return nil, err
	}
	a := &api{p: map[string]*windows.Proc{}}
	for _, name := range []string{"config_new", "config_free", "config_set_initial_size", "config_set_agent_timeout", "open", "conin_name", "conout_name", "spawn_config_new", "spawn_config_free", "spawn", "set_size", "free", "error_code", "error_free"} {
		proc, err := dll.FindProc("winpty_" + name)
		if err != nil {
			return nil, err
		}
		a.p[name] = proc
	}
	return a, nil
}

// Keep Go pointers passed through the uintptr FFI wrapper alive and stable.
//
//go:uintptrescapes
func (a *api) call(name string, args ...uintptr) uintptr {
	v, _, _ := a.p[name].Call(args...)
	return v
}
func (a *api) failure(action string, e uintptr) error {
	if e == 0 {
		return fmt.Errorf("WinPTY %s failed", action)
	}
	code := a.call("error_code", e)
	a.call("error_free", e)
	return fmt.Errorf("WinPTY %s failed (code %d)", action, code)
}

type PTY struct {
	mu      sync.Mutex
	api     *api
	handle  uintptr
	in, out *os.File
}

func (p *PTY) Fd() uintptr                 { return 0 } // Not a ConPTY handle; Resize is its own API.
func (p *PTY) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *PTY) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *PTY) Resize(cols, rows uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == 0 {
		return nil
	}
	var e uintptr
	if p.api.call("set_size", p.handle, uintptr(cols), uintptr(rows), uintptr(unsafe.Pointer(&e))) == 0 {
		return p.api.failure("resize", e)
	}
	return nil
}
func (p *PTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == 0 {
		return nil
	}
	// winpty_free terminates the agent and attached console processes.
	p.api.call("free", p.handle)
	p.handle = 0
	return errorsJoin(p.in.Close(), p.out.Close())
}
func errorsJoin(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func Start(cmd *exec.Cmd, cols, rows uint16) (result *PTY, err error) {
	if cmd.Process != nil {
		return nil, fmt.Errorf("WinPTY: command already started")
	}
	a, err := newAPI()
	if err != nil {
		return nil, err
	}
	var e uintptr
	cfg := a.call("config_new", 0x8, uintptr(unsafe.Pointer(&e))) // allow service desktop creation
	if cfg == 0 {
		return nil, a.failure("configuration", e)
	}
	defer a.call("config_free", cfg)
	a.call("config_set_initial_size", cfg, uintptr(cols), uintptr(rows))
	a.call("config_set_agent_timeout", cfg, 5000)
	handle := a.call("open", cfg, uintptr(unsafe.Pointer(&e)))
	if handle == 0 {
		return nil, a.failure("start agent", e)
	}
	p := &PTY{api: a, handle: handle}
	success := false
	defer func() {
		if !success {
			a.call("free", handle)
			if p.in != nil {
				p.in.Close()
			}
			if p.out != nil {
				p.out.Close()
			}
		}
	}()
	openPipe := func(name string, access uint32) (*os.File, error) {
		ptr := a.call(name, handle)
		if ptr == 0 {
			return nil, fmt.Errorf("WinPTY missing %s", name)
		}
		h, err := windows.CreateFile((*uint16)(unsafe.Pointer(ptr)), access, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(h), name), nil
	}
	p.in, err = openPipe("conin_name", windows.GENERIC_WRITE)
	if err != nil {
		return nil, err
	}
	p.out, err = openPipe("conout_name", windows.GENERIC_READ)
	if err != nil {
		return nil, err
	}
	app, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, err
	}
	args := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		args[i] = syscall.EscapeArg(arg)
	}
	line, err := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return nil, err
	}
	var cwd *uint16
	if cmd.Dir != "" {
		cwd, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, err
		}
	}
	env, err := environmentBlock(cmd.Env)
	if err != nil {
		return nil, err
	}
	spawn := a.call("spawn_config_new", 1, uintptr(unsafe.Pointer(app)), uintptr(unsafe.Pointer(line)), uintptr(unsafe.Pointer(cwd)), uintptr(unsafe.Pointer(&env[0])), uintptr(unsafe.Pointer(&e)))
	runtime.KeepAlive(app)
	runtime.KeepAlive(line)
	runtime.KeepAlive(cwd)
	runtime.KeepAlive(env)
	if spawn == 0 {
		return nil, a.failure("spawn configuration", e)
	}
	defer a.call("spawn_config_free", spawn)
	var process windows.Handle
	var systemError uint32
	if a.call("spawn", handle, spawn, uintptr(unsafe.Pointer(&process)), 0, uintptr(unsafe.Pointer(&systemError)), uintptr(unsafe.Pointer(&e))) == 0 {
		return nil, a.failure("spawn", e)
	}
	defer windows.CloseHandle(process)
	// Hold the original handle until FindProcess opens its own handle, preventing
	// PID recycling even when the child exits immediately. Only terminal.Process waits.
	pid, err := windows.GetProcessId(process)
	if err != nil {
		return nil, err
	}
	cmd.Process, err = os.FindProcess(int(pid))
	if err != nil {
		return nil, err
	}
	success = true
	return p, nil
}

func environmentBlock(env []string) ([]uint16, error) {
	if env == nil {
		env = os.Environ()
	}
	// Preserve the caller's complete environment, including an explicitly empty
	// one. Windows environment names compare case-insensitively; last wins.
	values := map[string]string{}
	for _, entry := range env {
		if strings.ContainsRune(entry, 0) {
			return nil, fmt.Errorf("NUL in child environment")
		}
		at := strings.IndexByte(entry, '=')
		if at == 0 {
			at = strings.IndexByte(entry[1:], '=') + 1
		}
		if at <= 0 {
			continue
		}
		values[strings.ToUpper(entry[:at])] = entry
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var block []uint16
	for _, k := range keys {
		block = append(block, utf16.Encode([]rune(values[k]))...)
		block = append(block, 0)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}
