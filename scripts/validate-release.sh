#!/usr/bin/env bash

set -euo pipefail

release_tag="${1:-}"

if [[ ! "$release_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?(\+[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]]; then
  echo "Invalid semantic-version tag: $release_tag" >&2
  exit 1
fi

if ! git show-ref --verify --quiet "refs/tags/$release_tag"; then
  echo "Tag does not exist: $release_tag" >&2
  exit 1
fi

tag_commit="$(git rev-list -n 1 "$release_tag")"

if [[ -z "$tag_commit" ]]; then
  echo "Could not resolve tag: $release_tag" >&2
  exit 1
fi

if git show-ref --verify --quiet refs/remotes/origin/main; then
  if ! git merge-base --is-ancestor "$tag_commit" refs/remotes/origin/main; then
    echo "Tag $release_tag does not point to a commit reachable from main." >&2
    exit 1
  fi
fi

module_path="$(go list -m -f '{{.Path}}')"
version_without_prefix="${release_tag#v}"
major="${version_without_prefix%%.*}"

if (( major >= 2 )) && [[ "$module_path" != */v"$major" ]]; then
  echo "Tag $release_tag requires module path suffix /v$major; found $module_path." >&2
  exit 1
fi

echo "Validated $release_tag at $tag_commit for module $module_path."
