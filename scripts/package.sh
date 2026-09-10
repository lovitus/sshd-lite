#!/usr/bin/env bash
set -euo pipefail
source_dir=${1:?Usage: package.sh SOURCE OUTPUT VERSION}
mkdir -p "$2"
output=$(cd "$2" && pwd)
version=${3:?Version required}
cd "$source_dir"
for target in linux/amd64 linux/arm64 linux/arm darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}; arch=${target#*/}
  suffix=$arch
  [[ "$arch" != arm ]] || suffix=armv7
  name="sshd-lite_${version}_${os}_${suffix}"
  stage=$(mktemp -d)
  binary=sshd-lite
  [[ "$os" != windows ]] || binary=sshd-lite.exe
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOARM=7 go build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$stage/$binary" .
  cp LICENSE UPSTREAM_RELEASE UPSTREAM_COMMIT PATCH_REPOSITORY_COMMIT "$stage/"
  if [[ "$os" == windows ]]; then
    (cd "$stage" && zip -q "$output/$name.zip" ./*)
  else
    tar -czf "$output/$name.tar.gz" -C "$stage" .
  fi
  rm -r "$stage"
done
