import { describe, expect, it } from "vitest";
import { CHANGELOG } from "@/content/changelog";
import { normalizeVersion } from "@/lib/whats-new";

// Release gate, run by `make release-check VERSION=vX.Y.Z` and by the
// release-gate job in .github/workflows/release.yml before anything is built
// or published. Skipped in normal test runs (env unset). In Vitest,
// import.meta.env carries the process environment.
const releaseVersion: string | undefined = import.meta.env.KAFKITO_RELEASE_VERSION || undefined;

function isRealIsoDate(date: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return false;
  const parsed = new Date(`${date}T00:00:00Z`);
  return !Number.isNaN(parsed.getTime()) && parsed.toISOString().slice(0, 10) === date;
}

describe("release gate", () => {
  it("rejects impossible calendar dates", () => {
    expect(isRealIsoDate("2026-09-25")).toBe(true);
    expect(isRealIsoDate("2026-02-30")).toBe(false);
    expect(isRealIsoDate("2026-9-25")).toBe(false);
  });

  it.skipIf(!releaseVersion)(
    `CHANGELOG has a dated entry for ${releaseVersion ?? "<unset>"}`,
    () => {
      const version = normalizeVersion(releaseVersion ?? "");
      const entry = CHANGELOG.find((e) => e.version === version);
      expect(
        entry,
        `no entry for version "${version}" in frontend/src/content/changelog.ts`,
      ).toBeDefined();
      expect(
        isRealIsoDate(entry?.date ?? ""),
        `entry "${version}" has no valid YYYY-MM-DD date (got "${entry?.date}")`,
      ).toBe(true);
      expect(entry?.items.length ?? 0).toBeGreaterThan(0);
    },
  );
});
