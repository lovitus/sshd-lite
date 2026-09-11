# sshd-lite with virtual password users

Portable SSH terminal server based on [jpillora/sshd-lite](https://github.com/jpillora/sshd-lite), with `SSHD_LITE_AUTH` and `@credential-file` authentication. Virtual login names need no system accounts. Shells run as the account that starts the server.

This repository maintains a small reviewed patch and a GitHub Actions pipeline. It checks the latest published stable upstream Release every six hours and fetches its exact tag, applies the patch, tests on Linux, macOS and Windows, and publishes binaries only after successful validation. An upstream conflict or failed test fails the workflow and leaves the last successful release available.

## Download

Download from this repository's **Releases** page. Each release includes:

- Linux amd64, arm64, and armv7 executables in `.tar.gz` archives (CGO disabled).
- macOS amd64 and arm64 executables in `.tar.gz` archives.
- Windows amd64 and arm64 executables (`sshd-lite.exe`) in `.zip` archives.
- The complete patched source archive, its exact upstream commit, the patch repository commit, and `SHA256SUMS`.

Extract the archive for your machine. The executable inside is named `sshd-lite`.
Windows uses PowerShell and ConPTY for interactive terminals on Windows 10 1809 / Server 2019 or newer. When ConPTY APIs are missing, it automatically uses a basic pipe shell; connect with `ssh -T`. This supports line-oriented commands, not full-screen programs, terminal resizing or terminal Ctrl-C behavior. `--no-pty` forces the same mode for troubleshooting. The Go runtime still requires Windows 10 / Server 2016 or newer; Windows 7/8 and Server 2012 are not supported. Windows credential files are checked using their actual Windows DACL, not Unix mode bits.

## Environment authentication

```sh
export SSHD_LITE_AUTH='bob:REPLACE_WITH_A_LONG_PASSWORD'
./sshd-lite --host 127.0.0.1 --port 22222
```

Connect from another terminal:

```sh
ssh -p 22222 bob@127.0.0.1
```

The environment name uses underscores; `export sshd-lite=...` is invalid shell syntax. For multiple users, separate records with literal newlines:

```sh
export SSHD_LITE_AUTH='bob:REPLACE_WITH_BOBS_PASSWORD
ci:REPLACE_WITH_CIS_PASSWORD'
```

For interactive setup without entering a password into shell history (Bash):

```bash
read -r -s -p 'Password: ' password
printf '\n'
export SSHD_LITE_AUTH="bob:$password"
unset password
```

## Credential file and merge

```sh
umask 077
mkdir -p "$HOME/vssh"
chmod 700 "$HOME/vssh"
touch "$HOME/vssh/users.txt"
chmod 600 "$HOME/vssh/users.txt"
```

Edit `users.txt` with one `username:password` record per line, for example:

```text
alice:REPLACE_WITH_ALICES_PASSWORD
backup:REPLACE_WITH_BACKUPS_PASSWORD
```

Start with the file; environment users are merged automatically if the variable is set:

```sh
./sshd-lite --host 127.0.0.1 --port 22222 "@$HOME/vssh/users.txt"
```

- Environment records override matching file usernames; the old file password stops working for that username.
- Duplicate names within one source are errors. Names are case-sensitive.
- Empty or malformed explicitly configured sources fail startup. Use `unset SSHD_LITE_AUTH` to disable the environment source.
- The first colon separates username and password. Password spaces and additional colons are preserved. LF and CRLF files work; blank lines are ignored; comments are not supported.
- Each source is limited to 16 KiB; the merged user set is limited to 1,024 users.
- Files must be regular files. On Unix, no group/other permissions are allowed (normally `0600` or `0400`). On Windows, the owner and every access-granting DACL entry must be the current user, SYSTEM or Administrators; missing/null DACLs and unsupported access-entry forms are rejected.
- Credentials load at startup; restart to reload. Existing sessions are not revoked.
- Existing CLI password, authorized-key, GitHub-key and `none` modes remain available when the variable is unset. Mixing environment passwords with key or `none` modes fails startup.

## Persistent host identity

Without `--keyfile` or `--keyseed`, the CLI creates a random Ed25519 host key once and reuses it across restarts. The default path belongs to the account running the daemon:

- Windows: `%AppData%\sshd-lite\host_key`
- Linux: `$XDG_CONFIG_HOME/sshd-lite/host_key`, or `$HOME/.config/sshd-lite/host_key`
- macOS: `$HOME/Library/Application Support/sshd-lite/host_key`

The startup log prints the actual path. Changing the working directory or replacing the executable does not change the key. Keep this file when upgrading and persist the config directory when running in a container. Changing the service account or its config-directory environment can select a different key. Separate instances under the same account share the default key; use separate explicit `--keyfile` paths if they need distinct identities.

Unix keys are created with mode `0600`; Windows keys have a private DACL from creation. A corrupt, unreadable or insecure default key stops startup rather than silently generating a replacement. Concurrent first startups atomically select the same completed key. The configuration filesystem must support hard links (such as NTFS on Windows); otherwise provide an existing `--keyfile` on a suitable filesystem. Explicit `--keyfile` and `--keyseed` retain their existing behavior. The Go library default is unchanged; persistence is a CLI feature.

Upgrading from the old ephemeral default introduces one final new fingerprint. Verify it against the server's startup log before updating the specific `known_hosts` entry. Already configured persistent keys do not change.

To choose your own key location, generate a persistent host key once:

```sh
ssh-keygen -t ed25519 -N '' -f "$HOME/vssh/host_key"
./sshd-lite --host 127.0.0.1 --port 22222 \
  --keyfile "$HOME/vssh/host_key" --shell /bin/bash --workdir "$HOME" \
  "@$HOME/vssh/users.txt"
```

For connections from another machine, replace `127.0.0.1` with the intended network interface address. `--sftp` enables file transfer; `--tcp-forwarding` enables forwarding. Run `./sshd-lite --help` for upstream options.

For cron, place the executable at `$HOME/vssh/sshd-lite` and use:

```cron
@reboot umask 077; "$HOME/vssh/sshd-lite" --host 127.0.0.1 --port 22222 --keyfile "$HOME/vssh/host_key" --shell /bin/bash --workdir "$HOME" "@$HOME/vssh/users.txt" >>"$HOME/vssh/server.log" 2>&1
```

An interactive shell export does not configure cron's environment.

## Windows usage

Extract the Windows ZIP, then start from PowerShell:

```powershell
$env:SSHD_LITE_AUTH = 'bob:REPLACE_WITH_A_LONG_PASSWORD'
.\sshd-lite.exe --host 127.0.0.1 --port 22222
```

Connect with `ssh -p 22222 bob@127.0.0.1`. For multiple environment users, use a PowerShell multiline string. `Remove-Item Env:SSHD_LITE_AUTH` disables the environment source.

For a password file, create it and restrict its ACL before entering credentials:

```powershell
$path = Join-Path $HOME 'sshd-lite-users.txt'
New-Item -ItemType File -Path $path
$sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
$acl = [System.Security.AccessControl.FileSecurity]::new()
$acl.SetOwner($sid)
$acl.SetAccessRuleProtection($true, $false)
$acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new($sid, 'FullControl', 'Allow'))
Set-Acl -LiteralPath $path -AclObject $acl
notepad $path
.\sshd-lite.exe --host 127.0.0.1 --port 22222 "@$path"
```

Save as UTF-8 without a BOM, one `username:password` per line. Environment/file merge rules are identical on every platform. These virtual users run as the Windows account that launched the daemon.

## Authentication scope

Passwords from the file/environment do not enter the daemon's command-line arguments. `SSHD_LITE_AUTH` is consumed and unset before sessions start. It is still a plaintext input, and unsetting does not erase process memory or Linux's initial `/proc` environment. Virtual sessions share the daemon owner's privileges and are not isolated from that owner's files or processes. This patch does not add brute-force cooldown or system-user switching.

## Build locally

Install Git, an authenticated GitHub CLI (`gh`), and the Go version required by the fetched upstream `go.mod` (or newer):

```sh
scripts/prepare.sh .build/source
cd .build/source
go test -race -timeout 15m ./...
go vet ./...
go test -race -tags=integration -run 'TestVirtualAuthEndToEnd|TestCompatibilityEndToEnd' -count=1 -timeout 3m .
go build -o ../../sshd-lite .
```

`prepare.sh` only accepts a new destination. To reproduce a release, pass the tag from its `UPSTREAM_RELEASE` as a second argument and check out the patch repository commit recorded in that release first.

## Automatic updates and releases

`.github/workflows/release.yml` runs on pushes to `main`, every six hours, and manually from **Actions → Follow upstream, test and release → Run workflow**. The optional `upstream_release` input selects a published stable upstream Release tag; leaving it empty selects GitHub’s latest stable Release. Branches, bare commits, drafts and prereleases are not accepted. The source is fetched explicitly from `refs/tags/<release-tag>`, never from the Release’s `target_commitish` branch. Pull requests are tested but cannot publish.

Each release tag combines the upstream Release tag, its resolved commit, and a fingerprint of the patches/build scripts/workflow. New commits on upstream `master` do not trigger new releases. Documentation-only changes in this repository do not trigger new releases either. An already published combination is skipped. The pipeline downloads upstream once, applies patches with `git apply --check`, and uses that same source archive for testing and builds. Tests cover the full upstream suite with the race detector, vet, and actual password SSH logins plus interactive PTY sessions. On Windows, the upstream vendored ConPTY unsafe-pointer vet findings are excluded from the module-wide vet pass; our root authentication package still receives the normal vet checks. After all three source-test jobs pass, all archives are uploaded to a draft Release. Linux amd64, macOS arm64 and Windows amd64 runners then download the actual GitHub Release archives, verify SHA-256, extract them and execute the packaged programs without rebuilding the daemon. They also check that restarting the executable from a different working directory preserves its host fingerprint, and that `--no-pty` rejects PTY allocation while providing a basic shell with working stdin, output and exit status (PowerShell and cmd.exe on Windows). Missing ConPTY procedure detection is unit-tested on Windows. Hosted runners use modern Windows; these tests do not constitute execution on an old Windows installation. They check environment-only, file-only and merged login, rejected passwords, process identity, credential environment cleanup, SFTP writes, exit status and interactive terminals. Only when every downloaded-executable test passes is the Release published. Linux arm/arm64, macOS amd64 and Windows arm64 are cross-compiled; they do not yet have native runtime coverage.

Only the built-in `GITHUB_TOKEN` is required; no personal token or stored user password is required by the workflow. Repository contents write permission is limited to draft upload/download and publication jobs. GitHub requires push access to see draft releases; the download job supplies its token only to the download step and disables persisted checkout credentials. GitHub handles scheduling and failure notifications; scheduled runs may be delayed. Check Actions if releases stop appearing. A new upstream Release must pass validation: patch conflicts and test failures require maintenance.

To update the patch, prepare the last compatible upstream in a disposable checkout, edit and test the source, then regenerate `patches/0001-virtual-password-auth.patch` using `git diff` against that upstream. Keep upstream source workflows out of this repository's workflow directory.

Upstream code and the derivative patch are MIT licensed; see `LICENSE`.
