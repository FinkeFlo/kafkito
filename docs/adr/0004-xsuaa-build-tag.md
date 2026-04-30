# ADR-0004: XSUAA as a build-tagged plugin

- **Status:** Accepted
- **Date:** 2026-04-30

## Context

ADR-0003 listed XSUAA support under `pkg/auth/xsuaa`. Implementation
revealed two concerns:
1. Most kafkito users do not deploy on SAP BTP. Compiling XSUAA into
   the default binary adds dependencies (BTP-specific JWT issuer,
   role-collection mapping) for no benefit.
2. Placing XSUAA under `pkg/` would create an external Go API surface
   we are not ready to maintain.

## Decision

XSUAA lives under `internal/auth/xsuaa/` with `//go:build btp` on every
file. A registry pattern in `internal/auth/mode.go` lets the XSUAA mode
register itself only when the `btp` build tag is set.

- Default build (`go build ./...`): no XSUAA code, no XSUAA dependencies.
- BTP build (`go build -tags btp ./...`): XSUAA mode registered and selectable.

CI builds and publishes both image variants on every release tag:
- `ghcr.io/finkeflo/kafkito:vX.Y.Z`       (default)
- `ghcr.io/finkeflo/kafkito:vX.Y.Z-btp`   (btp)

## Consequences

**Positive**
- Default binary stays minimal.
- Internal package status keeps the API surface fluid.
- One source tree, two binaries — no fork.

**Negative**
- BTP users must pull the `-btp` tag (one-time documentation note).
- A second image variant doubles release CI runtime by ~50%.

## Alternatives considered

- `pkg/auth/xsuaa` (ADR-0003 line 34). Rejected — premature public API.
- Runtime plugin via Go plugin system. Rejected — Go plugins are platform-fragile.
- Separate `kafkito-btp` repository. Rejected — keeps source unified, simpler maintenance.
