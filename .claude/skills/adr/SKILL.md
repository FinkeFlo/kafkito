---
name: adr
description: Write a new Architecture Decision Record in docs/adr/ in the repository's format, or add a dated amendment to an existing one, and register it in README.md and mkdocs.yml. Use when a decision about the stack, the architecture, the API contract, build variants or deployment needs recording, or when an ADR no longer matches the code.
---

# Architecture decision records

Existing ADRs: `docs/adr/0001` to `0005`. Read the newest one for the voice.

## A new ADR

1. File `docs/adr/000N-kebab-case-title.md`, next free number.
2. Structure, as in the existing files:

   ```markdown
   # ADR-000N: Title

   - **Status:** Accepted
   - **Date:** YYYY-MM-DD
   - **Supersedes:** (optional) the row or ADR it replaces

   ## Context
   ## Decision
   ## Consequences
   **Positive** / **Negative** (bullets; optional **Mitigation**)
   ## Alternatives considered
   ```

   Name the files, packages and commands the decision touches. Facts, not
   prose; English; no emojis.
3. Register it: the "Project Status" list in `README.md`
   (`- [x] ADR-000N: Title`), the "Architecture Decisions" nav in
   `mkdocs.yml`, and a link from `docs/architecture.md` where the decision
   changes what that page describes.
4. When it supersedes part of an older ADR, add an amendment to the older one
   that points forward (ADR-0002 does this for ADR-0005).

## An amendment

Decisions are not rewritten. Append `## Amendments` (or a new
`### YYYY-MM-DD` under it) with numbered corrections that state what the
code does now, as ADR-0002 and ADR-0003 do. Update `docs/architecture.md`
and `README.md` if they repeat the outdated statement.
