# Accepted implementation constraint

The user requires a standalone executable and explicitly authorizes runtime extraction. The implementation therefore embeds the official WinPTY x64 DLL and agent, extracts to a unique process-private directory only when fallback is selected, and cleans up on graceful exit. Default ConPTY behavior is retained. Rust/Python wrappers and separate console scrapers were also considered; neither offers a smaller integration or better established legacy compatibility. The earlier sidecar packaging recommendation below is superseded by this requirement.

# Windows PTY compatibility research — 2026-09-11

## Decision

For actual pre-ConPTY interactive terminal support, prefer a Windows-only backend adapter selecting system ConPTY first and native WinPTY second. Keep SSH session logic backend-agnostic. Pipe shells are a separate non-PTY capability, not a replacement for a terminal. No implementation or new release is made by this research.

A packaged `winpty.dll` and `winpty-agent.exe` is simpler to operate and audit than embedding and extracting them at runtime. For Windows x64, ship these beside the executable in a fixed private backend directory, pinned to a known build and included in checksums and license provenance. Do not search client-controlled environment variables, PATH or the current working directory for DLLs. ARM64 must retain its native ConPTY path until a compatible native legacy backend is validated; the examined assets are x64, not ARM64.

## Inspected candidates

- `rprichard/winpty`: native C++ library plus hidden-console agent; supports console input/output translation on older Windows. Official latest release 0.4.3 dated 2017-05-17. Old release does not itself establish unsuitability, but ongoing maintenance becomes our responsibility. https://github.com/rprichard/winpty
- `iamacarpet/go-winpty`: Go DLL wrapper, no application CGO requirement; requires DLL and agent. Module explicitly deprecated in favor of ConPTY. Close/resize lifecycle needs an adapter with synchronization and one owner. https://github.com/iamacarpet/go-winpty
- `gsw945/ptyterminal-go`, inspected commit `8b1783886e3c539dcf81d5c590519353dad28db1`: actual ConPTY-to-WinPTY auto-selection in `ptygo/start_windows.go`. Not recommended as an unmodified dependency: `buildEnv` always merges os.Environ, violating complete-environment semantics required by sshd-lite's no-inherit-env; interface documents possible dropped early input; Close and Resize do not share a lifecycle lock; DLL lookup includes environment and cwd; bundled binaries are x86-64; no tests in core ptygo directory found. These are source findings, not demonstrated Windows exploits or measured reliability rates. https://github.com/gsw945/ptyterminal-go
- `aymanbagabas/go-pty`, inspected commit `da4e5b0c5d98251e5432e8f18e460bee673d2efe`: clean cross-platform API with ConPTY backend; does not implement legacy WinPTY fallback. Useful modernization candidate, but replacing the current backend with it alone cannot fix the reported missing API. https://github.com/aymanbagabas/go-pty
- `microsoft/node-pty`: current README explicitly removes WinPTY support and requires Windows 1809+; adds native Node bindings and therefore is not a solution for this Go daemon. https://github.com/microsoft/node-pty
- Windows OpenSSH historical ssh-shellhost: separate console/VT bridging process; useful architectural precedent, not a documented stable generic PTY library interface. https://github.com/PowerShell/Win32-OpenSSH/wiki/TTY-PTY-support-in-Windows-OpenSSH

## Clean integration boundary

Existing `internal/terminal.Process` owns process wait, kill and terminal release. This is the appropriate boundary for backend selection. Its current `Start` assumes `exec.Cmd.Process`, while the vendored ConPTY backend also has private asynchronous ownership. A replacement must adapt lifecycle explicitly; returning only an io.ReadWriteCloser is insufficient.

Define only the capabilities already required: Read, Write, Resize, Wait/result, Kill, Close. Preserve exact Env and Cwd. Select the backend once before child start. Only unsupported ConPTY API should trigger legacy fallback; malformed command, permission or workdir failures must remain errors. Close and Resize must be serialized and Close idempotent. Keep a single reaper, bounded disconnect cleanup, output-before-exit ordering and initial input readiness.

The SSH session layer should not decide DLL availability, choose Windows shell-specific arguments or know WinPTY. True PTY sessions should accept normal SSH PTY requests; explicit non-PTY sessions remain a different path. Do not hide an ordinary pipe behind a PTY interface just to shrink a patch.

## Patch layout

Separate virtual authentication, persistent host identity, Windows terminal backend integration and their tests/build assets. Add owned helper source through a source overlay; use narrow contextual patches only for upstream hooks. Do not claim fewer total lines by merely moving code: measure changed upstream locations and lifecycle responsibilities.

The host key feature should remain CLI initialization using existing Config.KeyBytes/KeyFile. It does not require changing the SSH key negotiation implementation. Secure initial creation, no-overwrite publication, invalid-key failure and restart reuse are correctness requirements; removing them is not a meaningful simplification. Its current root authentication ACL reuse could move to a shared private-file helper if both features need it, rather than duplicating security code.

## Required validation before replacement

Native Windows runner must force each PTY backend separately: immediately delivered input, PowerShell/cmd editing keys, Ctrl-C without killing the session, Unicode, resize, representative full-screen application, exit status, disconnect/child cleanup, repeated close/concurrent resize, exact environment and workdir. Run actual packaged artifacts with helper files present, missing, wrong architecture and from another cwd. A real Server 2016/pre-1809 Windows VM is still needed before claiming old-OS runtime verification. Modern Windows forced-WinPTY tests cover the code path, not the old kernel.

Evidence gathered in this research is source/documentation inspection and local PE architecture inspection. No candidate has been executed on Windows as part of this research.
