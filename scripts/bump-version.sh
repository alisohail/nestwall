#!/usr/bin/env bash
# Bumps VERSION (MAJOR.MINOR.PATCH) from the commits since the last v* tag and prints the new version.
# Conventional commits: "feat" bumps MINOR, "!" or "BREAKING CHANGE" bumps MAJOR, anything else bumps PATCH.
set -euo pipefail
cd "$(dirname "$0")/.."

current=$(tr -d '[:space:]' < VERSION)
if [[ ! "$current" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "VERSION must be MAJOR.MINOR.PATCH, got '$current'" >&2
  exit 1
fi
major=${BASH_REMATCH[1]}
minor=${BASH_REMATCH[2]}
patch=${BASH_REMATCH[3]}

last_tag=$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || true)
log=$(git log "${last_tag:+$last_tag..}HEAD" --format='%s%n%b')

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
printf '%s\n' "$next" > VERSION
sed -i -E "s/NESTWALL_VERSION:-[0-9]+\.[0-9]+\.[0-9]+/NESTWALL_VERSION:-$next/" deploy/compose.yaml
NEXT="$next" node -e '
const fs = require("fs");
for (const file of ["frontend/package.json", "frontend/package-lock.json"]) {
  const json = JSON.parse(fs.readFileSync(file, "utf8"));
  json.version = process.env.NEXT;
  if (json.packages && json.packages[""]) json.packages[""].version = process.env.NEXT;
  fs.writeFileSync(file, JSON.stringify(json, null, 2) + "\n");
}
'
echo "$bump bump: $current -> $next (since ${last_tag:-the first commit})" >&2
echo "$next"
