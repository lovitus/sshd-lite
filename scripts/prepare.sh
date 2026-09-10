#!/usr/bin/env bash
# Fetch one immutable upstream revision, then apply every reviewed local patch.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
dest=${1:?Usage: prepare.sh NEW_DESTINATION [UPSTREAM_REF]}
ref=${2:-master}
if [[ -e "$dest" ]]; then
  echo "Destination already exists: $dest" >&2
  exit 1
fi
git init -q "$dest"
git -C "$dest" remote add origin https://github.com/jpillora/sshd-lite.git
git -C "$dest" fetch --depth=1 origin "$ref"
git -C "$dest" checkout -q --detach FETCH_HEAD
upstream=$(git -C "$dest" rev-parse HEAD)
for patch in "$root"/patches/*.patch; do
  git -C "$dest" apply --check "$patch"
  git -C "$dest" apply "$patch"
done
printf '%s\n' "$upstream" > "$dest/UPSTREAM_COMMIT"
printf '%s\n' "$(git -C "$root" rev-parse HEAD)" > "$dest/PATCH_REPOSITORY_COMMIT"
echo "Prepared patched upstream $upstream in $dest"
