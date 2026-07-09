import { beforeEach, describe, expect, it } from "vitest";
import {
  getLastSeen,
  hasUnseen,
  markSeen,
  normalizeVersion,
} from "./whats-new";

describe("normalizeVersion", () => {
  it("strips a leading v and -btp/-local/-dev suffixes", () => {
    expect(normalizeVersion("v0.0.0-rc17")).toBe("0.0.0-rc17");
    expect(normalizeVersion("0.0.0-rc17-btp")).toBe("0.0.0-rc17");
    expect(normalizeVersion("0.0.0-rc17-local")).toBe("0.0.0-rc17");
    expect(normalizeVersion("0.0.0-dev")).toBe("0.0.0");
  });
});

describe("hasUnseen / seen-state", () => {
  beforeEach(() => window.localStorage.clear());

  it("is false when current is undefined", () => {
    expect(hasUnseen(undefined, null)).toBe(false);
  });
  it("is true when current differs from lastSeen", () => {
    expect(hasUnseen("0.0.0-rc17", null)).toBe(true);
    expect(hasUnseen("0.0.0-rc17", "0.0.0-rc16")).toBe(true);
  });
  it("is false once the current version is marked seen", () => {
    markSeen("0.0.0-rc17");
    expect(getLastSeen()).toBe("0.0.0-rc17");
    expect(hasUnseen("0.0.0-rc17", getLastSeen())).toBe(false);
  });
});
