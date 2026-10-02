# Releasing Huginn

A release is a `v*` tag on `main`. Pushing it runs
[`.github/workflows/release.yml`](../.github/workflows/release.yml): vet and
tests, then goreleaser builds static binaries for linux, macOS and Windows
(amd64 and arm64), archives them with the README, `docs/CONFIG.md` and the
examples, and publishes them in a GitHub release with `checksums.txt` and
notes generated from the commits (D-055).

## Version numbers

Semantic versioning, `vMAJOR.MINOR.PATCH`. Before 1.0 the config folder may
still change: a change that needs users to edit their folder bumps MINOR and
says so in the release notes; fixes bump PATCH. The first release is
`v0.1.0`. A tag with a suffix (`v0.2.0-rc.1`) is published as a prerelease.

`huginn --version` prints the tag the binary was built from.

## Before tagging

1. CI is green on the `main` commit to tag, every job, Windows included.
2. Manual checks that CI cannot do, on a real GKE cluster:
   - **Not logged in.** `gcloud auth revoke`, start `huginn <env>`: the
     screen says "Not logged in to <env>" with the plugin's reason and
     `gcloud auth login`, and nothing is printed over it. Run
     `gcloud auth login` in another terminal: Huginn reconnects by itself
     within 30 s.
   - **Windows**, same check in Windows Terminal: the credential plugin's
     stderr capture is not covered by an end-to-end test there.
   - The usual tour: services, logs of a busy service, zoom, `ctrl+e` to
     another environment, `prd` shows its red banner.
3. Dry run, nothing published:
   ```sh
   goreleaser release --snapshot --clean   # artifacts in dist/
   ./dist/huginn_linux_amd64_v1/huginn --demo
   ```

## Tagging

```sh
git checkout main && git pull
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

Then check the release page: six archives, `checksums.txt`, the notes. To
redo a failed release, delete the release and the tag on GitHub, fix on
`main`, and tag again.
