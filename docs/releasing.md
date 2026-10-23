# Releasing go-sip

Git tags trigger the `publish-module` job in `.github/workflows/release.yml`. It requests the version from the public Go proxy and compiles it from a clean external module. Only then does `publish-release` create the GitHub Release and its notes.

## Prepare a release

1. Update `CHANGELOG.md` and the README status table.
2. Run `go mod tidy`, `go test -race ./...`, and `go vet ./...`.
3. Confirm the required CI checks passed on the commit to tag.
4. Choose a semantic version. Use `v0.x.y` while the API remains unstable and a suffix such as `-rc.1` for a prerelease.
5. Create an annotated tag on a commit reachable from `main` and push that exact tag.

```bash
git tag -a v0.3.0-rc.1 -m "v0.3.0-rc.1"
git push origin v0.3.0-rc.1
```

The workflow resolves the tag to a commit, runs the reusable validation workflow against that commit, publishes and validates the Go module through the public proxy, then creates release notes and marks prereleases correctly.

## Existing tags

Workflow files are not run retroactively when they are added. To create a GitHub Release for an existing tag, run the Release workflow manually and provide the exact tag. The workflow checks out and validates the historical source without moving or recreating the tag.

If historical code cannot pass its applicable build requirements, leave the tag unchanged and publish a corrected version. Do not claim that old code passed checks that did not exist at the time.

## Reruns and failures

The release job is idempotent: an existing GitHub Release is left unchanged. A failed module-proxy lookup can be rerun after indexing catches up. A failed validation requires a new commit and a new version tag because published tags must remain immutable.

If a published module version is unusable, add a `retract` directive to `go.mod` and publish a later version containing the retraction.

## Major versions

Starting with `v2`, the Go module path and internal imports must contain the `/v2` suffix. The release validation rejects a `v2` or later tag when the module path does not match.
