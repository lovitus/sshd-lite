#!/usr/bin/env bash
set -euo pipefail
source_dir=${1:?Usage: package.sh SOURCE OUTPUT VERSION}
mkdir -p "$2"
output=$(cd "$2" && pwd)
version=${3:?Version required}
cd "$source_dir"
for target in linux/amd64 linux/arm64 linux/arm darwin/amd64 darwin/arm64; do
  os=${target%/*}; arch=${target#*/}
  suffix=$arch
  [[ "$arch" != arm ]] || suffix=armv7
  name="sshd-lite_${version}_${os}_${suffix}"
  stage=$(mktemp -d)
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOARM=7 go build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$stage/sshd-lite" .
  cp LICENSE UPSTREAM_RELEASE UPSTREAM_COMMIT PATCH_REPOSITORY_COMMIT "$stage/"
  tar -czf "$output/$name.tar.gz" -C "$stage" .
  rm -r "$stage"
done
