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
   Requires Go, Bun and golangci-lint on your PATH.
3. Changing the HTTP API? Edit `api/openapi.yaml` (the contract, see ADR-0005)
   and run `make api-generate` to refresh the generated frontend types.
4. Open a PR with a clear description and a Test plan.
5. Sign off your commits (`-s`).

## Style

- Backend: idiomatic Go 1.26, golangci-lint clean.
- Frontend: Tailwind tokens from `@theme`; the default palette is disabled there. See `docs/DESIGN_GUIDELINES.md`.
- UI strings and code comments are English only. No emojis in UI chrome, logs, or commit messages.

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
