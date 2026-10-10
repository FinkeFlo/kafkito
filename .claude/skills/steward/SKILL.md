---
name: steward
description: Pull request stewardship for kafkito: how to open, update and babysit a PR (DCO sign-off, Conventional Commit subjects, draft PRs because of the owner auto-merge workflow, the CI job to make target map, what to do on red CI and review comments). Use when opening or updating a PR, reacting to CI failures or reviews, or asked to watch a PR.
---

# PR stewardship

## Opening and updating a PR

- Branch from `main`; commit with `git commit -s` (DCO). Subjects are
  Conventional Commits written for humans (`fix(server): return generic
  errors for cluster status`); they become the release notes.
- Open the PR as a **draft** and leave it a draft. `auto-merge-from-owner.yml`
  enables auto-merge (rebase) for every non-draft PR by the owner, and cloud
  sessions act with the owner's identity. Never mark a PR ready for review,
  never enable auto-merge, never merge.
- Body in English, no emojis: what changed and why, `Fixes #N` where it
  applies, a `## Test plan` with the commands run and their result, every
  `TODO(backend):` added, every deviation from `docs/DESIGN_GUIDELINES.md`,
  and what could not be run in this environment (Docker: `make e2e`).
- Rebase merges keep every commit: keep commits coherent and signed. Amend or
  rebase only commits that are not pushed yet; the hooks refuse force pushes,
  so a pushed branch only moves forward (merge `origin/main` into it for
  conflicts, never rebase).

## CI jobs and their local equivalents

| Job (workflow) | Runs | Reproduce locally |
|---|---|---|
| `go` matrix `""`, `btp`, `devauth` (`ci.yml`) | `go build -tags X ./...`, `make test TAGS=X`, golangci-lint `--build-tags=X`, `make lint-version-check`, `goreleaser check` | `go build -tags X ./... && make test TAGS=X && bin/golangci-lint run --build-tags=X ./...`; `make lint-version-check` |
| `frontend` (`ci.yml`) | `make api-check`, `make frontend-check` | the same |
| `api-breaking` (`ci.yml`, PRs) | oasdiff on `api/openapi.yaml` against the base, fails on ERR | `git diff origin/main...HEAD -- api/openapi.yaml`; a removed or narrowed path, field, enum value or type is breaking |
| `docker-build` (`ci.yml`) | `docker build` with `BUILD_TAGS` `""` and `btp` | `make docker-build` (needs Docker) |
| `actionlint` (`ci.yml`) | `make actionlint` | the same |
| `dco` (`ci.yml`, PRs) | every commit has `Signed-off-by` | `git log --format='%B' origin/main..HEAD \| grep Signed-off-by` |
| `playwright` (`e2e.yml`) | `make e2e` (Playwright + axe) | `make e2e` (needs Docker, curl, jq); otherwise read the job log and the uploaded `playwright-report` |
| `trufflehog`, `denylist` (`secret-scan.yml`) | secret and denylisted-identifier scans | never commit credentials; if one leaked, rotate it first (CONTRIBUTING.md, "Secret scanning") |
| `release-gate`, `goreleaser` (`release.yml`) | on `v*` tags only | not a PR concern; see `/release-prep` |

## Red CI

1. Open the failing job's log; find the first failure, not the last line.
2. Reproduce with the command from the table. Fix the cause in code or spec;
   never skip, disable or quarantine a test, never loosen a lint rule, never
   re-run a job as the "fix".
3. `api-breaking` red: decide whether the change must be breaking. If yes,
   say so in the PR and ask the maintainer; if no, make it additive.
4. `dco` red: a commit lacks the sign-off. New commits must carry `-s`; a
   pushed unsigned commit cannot be rewritten by an agent (force pushes are
   refused): report it and let the maintainer decide.
5. `playwright` red without Docker: read the report artifacts, fix what the
   failure shows (selectors, copy, a changed flow), and say in the PR that
   the fix is unverified locally.
6. Run `make check` before every push. One validated push beats three guesses.

## Review comments

- Small, local asks (nits, renames, a test, a one-function refactor): fix,
  push, reply briefly, resolve the thread.
- Larger or architectural asks, security findings, anything you are unsure
  about: reply with a proposal and ask; do not push ahead.
- Bot findings are claims to verify against the code, not orders.
- After addressing a changes-requested review, re-request the reviewer.

## Never

Mark ready, enable auto-merge, merge, push tags, force-push, delete remote
refs, use `--no-verify`, or edit generated files to make CI pass.
