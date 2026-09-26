# Contributing to kafkito

Thanks for considering a contribution!

## DCO

Every commit must include a `Signed-off-by:` trailer certifying the
[Developer Certificate of Origin](https://developercertificate.org/).
Add it automatically with `git commit -s`. The DCO check is enforced by
GitHub Actions.

## Workflow

1. Fork, branch, code.
2. `make check` — runs Go tests, golangci-lint, the OpenAPI lint and
   generated-types drift check (`make api-check`) and all frontend checks.
   Requires Go, Bun and curl on your PATH. `make lint` downloads the pinned
   golangci-lint release (`GOLANGCI_LINT_VERSION` in the `Makefile`, the same
   version CI uses) into `./bin` on first use and lints both the default and
   the `btp` build. To bump it, change the Makefile variable and the
   `golangci-lint-action` version in `.github/workflows/ci.yml` together
   (`make lint-version-check` enforces this). CI runs the same Makefile
   targets (`make test`, `make api-check`, `make frontend-check`); only
   golangci-lint runs via its GitHub Action, pinned to the same version.
   `make actionlint` lints the GitHub workflows.
3. Changing the HTTP API? Edit `api/openapi.yaml` (the contract, see ADR-0005)
   and run `make api-generate` to refresh the generated server interface and
   frontend types. A new endpoint then needs its strict handler method, its
   mount in `internal/server/api_routes.go` and an `apiOps` test entry; the
   steps are in
   [ADR-0005, "Adding an endpoint"](docs/adr/0005-openapi-contract.md#adding-an-endpoint).
4. Open a PR with a clear description and a Test plan.
5. Sign off your commits (`-s`).

Pure formatting commits are listed in `.git-blame-ignore-revs`. GitHub skips
them in blame views; locally run
`git config blame.ignoreRevsFile .git-blame-ignore-revs` once per clone.

## Pre-commit hooks

Optional. `.pre-commit-config.yaml` defines lean hooks that only look at
staged files:

- `gitleaks` — blocks credentials (see [Secret scanning](#secret-scanning)).
- `golangci-lint-fmt` — gofmt/goimports via the pinned golangci-lint in
  `./bin` (installed on first use, like `make lint`).
- `biome-check` — Biome lint and format check for the frontend sources
  (`frontend/src`, `frontend/e2e`, config files); needs
  `make frontend-install`.

Enable them once per clone:

```sh
brew install pre-commit   # or: pipx install pre-commit
pre-commit install
```

The formatter hook rewrites files in place and fails the commit; review and
stage the changes, then commit again. `make check` and CI remain the gate.

## Style

- Backend: idiomatic Go 1.26, golangci-lint clean.
- Frontend: Tailwind tokens from `@theme`; the default palette is disabled there. See `docs/DESIGN_GUIDELINES.md`.
- UI strings and code comments are English only. No emojis in UI chrome, logs, or commit messages.

## Releasing

Releases are cut manually by pushing a signed `v*` tag; everything else is
automated by `.github/workflows/release.yml` and `.goreleaser.yaml`.

1. In the release-prep commit, add an entry for the new version (without the
   leading `v`) and today's date (`YYYY-MM-DD`) to
   `frontend/src/content/changelog.ts`. This is the curated, user-facing
   "What's new" shown in the app. Check it with
   `make release-check VERSION=vX.Y.Z`.
2. Tag and push: `git tag -s vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z`.
3. The `release-gate` job fails the release before anything is built if step 1
   is missing. Then goreleaser builds the binaries and archives (with
   `SHA256SUMS` and SBOMs), pushes the `ghcr.io/finkeflo/kafkito` images
   (`vX.Y.Z`, `latest`, and the `-btp` / `-local` variants) and publishes the
   GitHub release. Tags with a pre-release suffix (e.g. `v1.2.0-rc.1`) are
   marked as pre-releases.

The GitHub release notes are generated from the Conventional Commit subjects
since the previous tag (grouped into Features, Bug Fixes, Security, Others and
Dependencies; `chore`, `ci`, `test`, `style` and `docs(changelog)` are left
out), so write commit subjects for humans.

`make release-snapshot` runs the whole pipeline locally without publishing
(requires goreleaser v2, Docker with buildx and syft); output goes to `./dist`.

## Secret scanning

Secrets must never enter the repo. Three layers protect against accidents:

1. **Local pre-commit hook (Gitleaks).** Enable the
   [pre-commit hooks](#pre-commit-hooks) once after cloning;
   `git commit` then aborts when Gitleaks finds a credential in the staged diff.
2. **CI scan (TruffleHog).** `.github/workflows/secret-scan.yml` scans every push to `main` and every PR. Verified findings fail the job.
3. **GitHub Push Protection.** Enabled in repo settings; blocks pushes containing recognised provider tokens server-side.

If a secret leaks despite this, **rotate the credential at the provider first**, then clean the history (`git filter-repo`).

## License

By submitting a contribution, you agree that your work is licensed under
the Apache-2.0 License (this project's outbound license), as certified by
the DCO sign-off.
