#!/usr/bin/env bash
#
# attach-android-apk.sh — attach the newest signed Android APK to a server
# release.
#
# Usage:
#   scripts/attach-android-apk.sh <server-tag>
#
# The Android app is published from its own android-v* releases, and those
# never take the Latest mark: the panel's self-update reads Latest and expects
# the server assets there. So Latest, the release people open, carried no
# Android app. The Release workflow runs this for each server tag, and Release
# Android runs it for the current Latest when it publishes a new APK.
#
# The APK is copied from the newest android-v* release, not rebuilt, and is
# checked against that release's android-checksums.txt before it is uploaded.
# Any other version of the APK already on the server release is removed after
# the upload, so the release carries one app and never none.
#
# DRY_RUN=1 downloads and checks the APK but changes nothing.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $(basename "$0") <server-tag>" >&2
  exit 1
fi
server_tag="$1"

releases="$(gh release list --limit 1000 --exclude-drafts --json tagName --jq '.[].tagName')"
android_tag="$(grep -E '^android-v[0-9]+\.[0-9]+\.[0-9]+$' <<<"$releases" | sort -V | tail -n 1 || true)"
if [[ -z "$android_tag" ]]; then
  echo "No published android-v* release; nothing to attach."
  exit 0
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

gh release download "$android_tag" --dir "$work" --pattern 'SFPanel-Android-*.apk' --pattern 'android-checksums.txt'
shopt -s nullglob
apks=("$work"/SFPanel-Android-*.apk)
if [[ ${#apks[@]} -ne 1 || ! -f "$work/android-checksums.txt" ]]; then
  echo "Error: $android_tag should carry one SFPanel-Android-*.apk and android-checksums.txt." >&2
  exit 1
fi
(cd "$work" && sha256sum --check --strict android-checksums.txt)
apk_name="$(basename "${apks[0]}")"

assets="$(gh release view "$server_tag" --json assets --jq '.assets[].name')"

if [[ "${DRY_RUN:-}" == 1 ]]; then
  echo "Would attach $apk_name and android-checksums.txt ($android_tag) to $server_tag."
else
  gh release upload "$server_tag" "${apks[0]}" "$work/android-checksums.txt" --clobber
  echo "Attached $apk_name ($android_tag) to $server_tag."
fi

while IFS= read -r asset; do
  [[ "$asset" =~ ^SFPanel-Android-.*\.apk$ && "$asset" != "$apk_name" ]] || continue
  if [[ "${DRY_RUN:-}" == 1 ]]; then
    echo "Would remove $asset from $server_tag."
  else
    gh release delete-asset "$server_tag" "$asset" --yes
    echo "Removed $asset from $server_tag."
  fi
done <<<"$assets"
