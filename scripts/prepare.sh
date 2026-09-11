#!/usr/bin/env bash
# Fetch a published stable upstream release tag, then apply reviewed patches.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
dest=${1:?Usage: prepare.sh NEW_DESTINATION [UPSTREAM_RELEASE_TAG]}
release_tag=${2:-}
if [[ -z "$release_tag" ]]; then
  release_tag=$(gh api repos/jpillora/sshd-lite/releases/latest --jq '.tag_name')
fi
# Verify even explicit inputs refer to a published stable release, not a branch.
release_tag=$(gh release view "$release_tag" --repo jpillora/sshd-lite   --json tagName,isDraft,isPrerelease   --jq 'if .isDraft or .isPrerelease then error("A published stable release is required") else .tagName end')
# Keep release and archive names portable across the supported platforms.
if [[ ! "$release_tag" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]]; then
  echo "Unsupported release tag name: $release_tag" >&2
  exit 1
fi
if [[ -e "$dest" ]]; then
  echo "Destination already exists: $dest" >&2
  exit 1
fi
git init -q "$dest"
git -C "$dest" remote add origin https://github.com/jpillora/sshd-lite.git
git -C "$dest" fetch --depth=1 origin "refs/tags/$release_tag"
git -C "$dest" checkout -q --detach FETCH_HEAD
upstream=$(git -C "$dest" rev-parse HEAD)
for patch in "$root"/patches/*.patch; do
  git -C "$dest" apply --check "$patch"
  git -C "$dest" apply "$patch"
done
# Owned implementation files stay outside contextual upstream patches.
if [[ -d "$root/overlay" ]]; then
  while IFS= read -r -d '' file; do
    relative=${file#"$root/overlay/"}
    if [[ -e "$dest/$relative" ]]; then
      echo "Overlay would overwrite upstream file: $relative" >&2
      exit 1
    fi
    mkdir -p "$(dirname "$dest/$relative")"
    cp "$file" "$dest/$relative"
  done < <(find "$root/overlay" -type f -print0)
fi
printf '%s\n' "$release_tag" > "$dest/UPSTREAM_RELEASE"
printf '%s\n' "$upstream" > "$dest/UPSTREAM_COMMIT"
printf '%s\n' "$(git -C "$root" rev-parse HEAD)" > "$dest/PATCH_REPOSITORY_COMMIT"
echo "Prepared patched upstream release $release_tag ($upstream) in $dest"
