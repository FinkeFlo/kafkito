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
   and run `make api-generate` to refresh the generated frontend types.
4. Open a PR with a clear description and a Test plan.
5. Sign off your commits (`-s`).

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

1. **Local pre-commit hook (Gitleaks).** Run once after cloning:
   ```sh
   brew install pre-commit   # or: pipx install pre-commit
   pre-commit install
   ```
   `git commit` then aborts when Gitleaks finds a credential in the staged diff.
2. **CI scan (TruffleHog).** `.github/workflows/secret-scan.yml` scans every push to `main` and every PR. Verified findings fail the job.
3. **GitHub Push Protection.** Enabled in repo settings; blocks pushes containing recognised provider tokens server-side.

If a secret leaks despite this, **rotate the credential at the provider first**, then clean the history (`git filter-repo`).

## License

By submitting a contribution, you agree that your work is licensed under
the Apache-2.0 License (this project's outbound license), as certified by
the DCO sign-off.
