#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release_test_directory="$(mktemp -d)"

cleanup() {
  rm -rf -- "$release_test_directory"
}

trap cleanup EXIT

cd "$release_test_directory"
git init --quiet
git config user.name "go-sip release test"
git config user.email "release-test@example.test"

mkdir -p scripts
cp "$repository_root/go.mod" .
cp "$repository_root/scripts/validate-release.sh" scripts/

git add go.mod scripts/validate-release.sh
git commit --quiet -m "release validation fixture"
git update-ref refs/remotes/origin/main HEAD

git tag v0.3.0-rc.1
bash scripts/validate-release.sh v0.3.0-rc.1

expect_rejection() {
  local description="$1"
  local tag="$2"

  if bash scripts/validate-release.sh "$tag" >/dev/null 2>&1; then
    echo "$description was unexpectedly accepted: $tag" >&2
    exit 1
  fi
}

expect_rejection "invalid semantic version" "invalid-tag"
expect_rejection "missing tag" "v0.3.1"

git tag v2.0.0
expect_rejection "major version without matching module path" "v2.0.0"

fixture_tree="$(git write-tree)"
unreachable_commit="$(printf '%s\n' "unreachable release fixture" | git commit-tree "$fixture_tree")"
git tag v0.4.0 "$unreachable_commit"
expect_rejection "tag outside main history" "v0.4.0"

echo "Release tag validation scenarios passed."
