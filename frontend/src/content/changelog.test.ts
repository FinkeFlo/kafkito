import { describe, expect, it } from "vitest";
import {
  CHANGELOG,
  MAX_CHANGELOG_DESCRIPTION_LENGTH,
  MAX_CHANGELOG_TITLE_LENGTH,
} from "@/content/changelog";
import { normalizeVersion } from "@/lib/whats-new";

const items = CHANGELOG.flatMap((entry) =>
  entry.items.map((item) => ({ version: entry.version, item })),
);

describe("changelog copy budget", () => {
  it("has items to check", () => {
    expect(items.length).toBeGreaterThan(0);
  });

  it.each(items)("v$version title fits one line: $item.title", ({ item }) => {
    expect(item.title.length).toBeLessThanOrEqual(MAX_CHANGELOG_TITLE_LENGTH);
  });

  it.each(items)(
    "v$version description fits two lines: $item.title",
    ({ item }) => {
      expect(item.description?.length ?? 0).toBeLessThanOrEqual(
        MAX_CHANGELOG_DESCRIPTION_LENGTH,
      );
    },
  );

  it.each(items)(
    "v$version description is not empty or a restatement: $item.title",
    ({ item }) => {
      if (item.description === undefined) return;
      expect(item.description.trim()).not.toBe("");
      expect(item.description.trim().toLowerCase()).not.toBe(
        item.title.trim().toLowerCase(),
      );
    },
  );
});

describe("changelog structure", () => {
  it("uses normalized version keys", () => {
    for (const entry of CHANGELOG) {
      expect(entry.version).toBe(normalizeVersion(entry.version));
      expect(entry.version.startsWith("v")).toBe(false);
    }
  });

  it("uses ISO dates", () => {
    for (const entry of CHANGELOG) {
      expect(entry.date).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    }
  });

  it("lists versions newest first by date", () => {
    const dates = CHANGELOG.map((e) => e.date);
    expect(dates).toEqual([...dates].sort().reverse());
  });

  it("has no duplicate versions", () => {
    const versions = CHANGELOG.map((e) => e.version);
    expect(new Set(versions).size).toBe(versions.length);
  });
});
