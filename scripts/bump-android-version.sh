#!/usr/bin/env bash
# Bumps mobile/android/VERSION from the commits that touched mobile/android since the last android-v* tag.
# Prints the new version, or nothing when the Android app has no changes. The first release uses the current version.
set -euo pipefail
cd "$(dirname "$0")/.."

file=mobile/android/VERSION
current=$(tr -d '[:space:]' < "$file")
if [[ ! "$current" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "$file must be MAJOR.MINOR.PATCH, got '$current'" >&2
  exit 1
fi

last_tag=$(git describe --tags --abbrev=0 --match 'android-v*' 2>/dev/null || true)
if [[ -z "$last_tag" ]]; then
  echo "$current"
  exit 0
fi
if git diff --quiet "$last_tag" HEAD -- mobile/android; then
  exit 0
fi

major=${BASH_REMATCH[1]}
minor=${BASH_REMATCH[2]}
patch=${BASH_REMATCH[3]}
log=$(git log "$last_tag..HEAD" --format='%s%n%b' -- mobile/android)

bump=patch
if grep -Eq '^[a-z]+(\([^)]*\))?!:|^BREAKING CHANGE' <<<"$log"; then
  bump=major
elif grep -Eq '^feat(\([^)]*\))?:' <<<"$log"; then
  bump=minor
fi

case $bump in
  major) major=$((major + 1)); minor=0; patch=0 ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac

next="$major.$minor.$patch"
printf '%s\n' "$next" > "$file"
echo "android $bump bump: $current -> $next (since $last_tag)" >&2
echo "$next"
