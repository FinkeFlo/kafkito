# frontend/dist

This directory holds the built single-page-app bundle, served by the Go
binary via `//go:embed all:dist` (see `frontend/embed.go`).

Two files are tracked in git as placeholders:

- `index.html` — minimal "frontend not built" page so `go build ./...`
  works on a fresh clone without first building the frontend.
- `README.md` — this file.

Everything else in this directory is gitignored. To produce the real
bundle:

```sh
make build         # builds frontend then the Go binary
# or, frontend only:
cd frontend && bun install && bun run build
```

After a build the placeholder `index.html` is overwritten with the real
SPA entry point. `git status` will show it as modified — that's expected;
do not commit the built version.
