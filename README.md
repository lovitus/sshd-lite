# sshd-lite with virtual password users

Portable SSH terminal server based on [jpillora/sshd-lite](https://github.com/jpillora/sshd-lite), with `SSHD_LITE_AUTH` and `@credential-file` authentication. Virtual login names need no system accounts. Shells run as the account that starts the server.

This repository maintains a small reviewed patch and a GitHub Actions pipeline. It checks the latest published stable upstream Release every six hours and fetches its exact tag, applies the patch, tests on Linux and macOS, and publishes binaries only after successful validation. An upstream conflict or failed test fails the workflow and leaves the last successful release available.

## Download

Download from this repository's **Releases** page. Each release includes:

- Linux amd64, arm64, and armv7 executables in `.tar.gz` archives (CGO disabled).
- macOS amd64 and arm64 executables in `.tar.gz` archives.
- The complete patched source archive, its exact upstream commit, the patch repository commit, and `SHA256SUMS`.

Extract the archive for your machine. The executable inside is named `sshd-lite`.
These releases target Linux and macOS. Windows binaries are not published: the supplied credential-file checks use Unix permissions and have not been adapted to Windows ACLs.

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
- Files must be regular files with no group/other permissions, normally `0600` or `0400`.
- Credentials load at startup; restart to reload. Existing sessions are not revoked.
- Existing CLI password, authorized-key, GitHub-key and `none` modes remain available when the variable is unset. Mixing environment passwords with key or `none` modes fails startup.

For continued use, generate a persistent host key once:

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

## Authentication scope

Passwords from the file/environment do not enter the daemon's command-line arguments. `SSHD_LITE_AUTH` is consumed and unset before sessions start. It is still a plaintext input, and unsetting does not erase process memory or Linux's initial `/proc` environment. Virtual sessions share the daemon owner's privileges and are not isolated from that owner's files or processes. This patch does not add brute-force cooldown or system-user switching.

## Build locally

Install Git, an authenticated GitHub CLI (`gh`), and the Go version required by the fetched upstream `go.mod` (or newer):

```sh
scripts/prepare.sh .build/source
cd .build/source
go test -race -timeout 15m ./...
go vet ./...
go test -race -tags=integration -run TestVirtualAuthEndToEnd -count=1 -timeout 3m .
go build -o ../../sshd-lite .
```

`prepare.sh` only accepts a new destination. To reproduce a release, pass the tag from its `UPSTREAM_RELEASE` as a second argument and check out the patch repository commit recorded in that release first.

## Automatic updates and releases

`.github/workflows/release.yml` runs on pushes to `main`, every six hours, and manually from **Actions → Follow upstream, test and release → Run workflow**. The optional `upstream_release` input selects a published stable upstream Release tag; leaving it empty selects GitHub’s latest stable Release. Branches, bare commits, drafts and prereleases are not accepted. The source is fetched explicitly from `refs/tags/<release-tag>`, never from the Release’s `target_commitish` branch. Pull requests are tested but cannot publish.

Each release tag combines the upstream Release tag, its resolved commit, and a fingerprint of the patches/build scripts/workflow. New commits on upstream `master` do not trigger new releases. Documentation-only changes in this repository do not trigger new releases either. An already published combination is skipped. The pipeline downloads upstream once, applies patches with `git apply --check`, and uses that same source archive for testing and builds. Tests cover the full upstream suite with the race detector, vet, and actual password SSH logins plus interactive PTY sessions. Only after both OS jobs pass are assets uploaded to a draft and the release published. Linux arm builds are cross-compiled; runtime tests execute on the GitHub Linux/macOS runners.

Only the built-in `GITHUB_TOKEN` is required; no personal token or stored user password is required by the workflow. Repository contents write permission is limited to the release job. GitHub handles scheduling and failure notifications; scheduled runs may be delayed. Check Actions if releases stop appearing. A new upstream Release must pass validation: patch conflicts and test failures require maintenance.

To update the patch, prepare the last compatible upstream in a disposable checkout, edit and test the source, then regenerate `patches/0001-virtual-password-auth.patch` using `git diff` against that upstream. Keep upstream source workflows out of this repository's workflow directory.

Upstream code and the derivative patch are MIT licensed; see `LICENSE`.
