---
name: release-prep
description: Prepare a kafkito release: add the dated entry to frontend/src/content/changelog.ts, verify it with make release-check and open the release-prep PR. Use when asked to prepare, cut or document a release or to bump the version. It never creates or pushes a tag; a human does that after the PR merged.
---

# Prepare a release

The release workflow (`.github/workflows/release.yml`) fails before building
anything unless `frontend/src/content/changelog.ts` has a dated entry for the
tag. Steps:

1. **Collect the changes** since the last release:
   `git describe --tags --abbrev=0` and
   `git log <last-tag>..HEAD --format='%s'`. Only user-visible changes go into
   the entry (`feat`, `fix`, security); `chore`, `ci`, `test`, `docs` do not.
2. **Add the entry** at the top of `CHANGELOG` in
   `frontend/src/content/changelog.ts`: `version` without the leading `v`
   (`"1.4.0"`, pre-releases like `"1.4.0-rc.1"`), `date` as `YYYY-MM-DD`
   (today), `items` with `type` `feature` | `fix` | `security`, a `title` of
   at most 70 characters and an optional `description` of at most 160
   (`changelog.test.ts` enforces both; prefer a sharper title over a title
   plus a sentence). Screenshots go to `frontend/public/whats-new/` and are
   referenced as `/whats-new/<file>.png`. English, sentence case, no emojis.
3. **Verify.**
   `cd frontend && bun run test src/content/changelog.test.ts` and, from the
   root, `make release-check VERSION=vX.Y.Z` (a wrong version must fail; try
   `VERSION=vX.Y.Z+1` once to see the gate work).
4. **Commit and PR.** `git commit -s -m "docs(changelog): add the X.Y.Z entry"`,
   then a draft PR titled the same, with the test plan from step 3. The
   maintainer merges it.
5. **Hand over.** The tag is a human step: after the merge the maintainer runs
   `git tag -s vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z` (CONTRIBUTING.md,
   "Releasing"). Do not create, push or suggest pushing the tag yourself; the
   hooks and permissions refuse tag pushes anyway.
