# Contributing to kafkito

Thanks for considering a contribution!

## DCO

Every commit must include a `Signed-off-by:` trailer certifying the
[Developer Certificate of Origin](https://developercertificate.org/).
Add it automatically with `git commit -s`. The DCO check is enforced by
GitHub Actions.

## Workflow

1. Fork, branch, code.
2. `make test && make lint` (Go); `cd frontend && bun run lint && bun run build && bun run check:palette` (frontend).
3. Open a PR with a clear description and a Test plan.
4. Sign off your commits (`-s`).

## Style

- Backend: idiomatic Go 1.26, golangci-lint clean.
- Frontend: Tailwind tokens from `@theme`, no default palette classes. See `docs/DESIGN_GUIDELINES.md`.
- UI strings and code comments are English only. No emojis in UI chrome, logs, or commit messages.

## License

By submitting a contribution, you agree that your work is licensed under
the Apache-2.0 License (this project's outbound license), as certified by
the DCO sign-off.
