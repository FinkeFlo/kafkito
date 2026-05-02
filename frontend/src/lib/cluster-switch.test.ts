import { describe, it, expect } from "vitest";
import { computeSwitchTarget } from "./cluster-switch";

describe("computeSwitchTarget", () => {
  it("preserves the section when switching from a topic detail page", () => {
    expect(
      computeSwitchTarget("/clusters/IAK/topics/foo/messages", "PROD AsPIRe Integration"),
    ).toBe("/clusters/PROD%20AsPIRe%20Integration/topics");
  });

  it("preserves the section when switching from a list page", () => {
    expect(computeSwitchTarget("/clusters/IAK/topics", "OTHER")).toBe(
      "/clusters/OTHER/topics",
    );
  });

  it("returns cluster dashboard when switching from cluster index", () => {
    expect(computeSwitchTarget("/clusters/IAK", "OTHER")).toBe("/clusters/OTHER");
  });

  it("preserves the security sub-tab when switching from /security/acls", () => {
    expect(computeSwitchTarget("/clusters/IAK/security/acls", "OTHER")).toBe(
      "/clusters/OTHER/security/acls",
    );
  });

  it("preserves the security sub-tab and drops the deep id from /security/users/Y", () => {
    expect(
      computeSwitchTarget("/clusters/IAK/security/users/alice", "OTHER"),
    ).toBe("/clusters/OTHER/security/users");
  });

  it("returns /security index when on the security layout root", () => {
    expect(computeSwitchTarget("/clusters/IAK/security", "OTHER")).toBe(
      "/clusters/OTHER/security",
    );
  });

  it("preserves /groups section even from group-detail item path", () => {
    expect(
      computeSwitchTarget("/clusters/IAK/groups/g/members", "OTHER"),
    ).toBe("/clusters/OTHER/groups");
  });

  it("URL-encodes cluster names with spaces", () => {
    expect(
      computeSwitchTarget("/clusters/A/topics", "Has Spaces & Symbols"),
    ).toBe("/clusters/Has%20Spaces%20%26%20Symbols/topics");
  });

  it("ignores trailing query strings — caller passes pathname only", () => {
    // contract: input is the pathname, query is dropped
    expect(
      computeSwitchTarget("/clusters/IAK/topics/foo/messages", "OTHER"),
    ).toBe("/clusters/OTHER/topics");
  });
});
