import { describe, expect, it } from "vitest";
import { MAX_DEPTH, MAX_PATHS } from "./path-tree";
import { buildXmlPathTree, looksLikeXml } from "./xml-path-tree";

describe("looksLikeXml", () => {
  it("accepts values starting with <", () => {
    expect(looksLikeXml("<order/>")).toBe(true);
    expect(looksLikeXml("  <order/>")).toBe(true);
  });

  it("rejects non-XML values", () => {
    expect(looksLikeXml("{}")).toBe(false);
    expect(looksLikeXml("plain text")).toBe(false);
    expect(looksLikeXml("")).toBe(false);
    expect(looksLikeXml(undefined)).toBe(false);
  });
});

describe("buildXmlPathTree", () => {
  it("returns an empty tree for no samples", () => {
    expect(buildXmlPathTree([]).size).toBe(0);
  });

  it("indexes the root element and a scalar leaf child", () => {
    const tree = buildXmlPathTree(["<order><status>shipped</status></order>"]);

    expect(tree.get("//order")).toMatchObject({ type: "object" });
    expect(tree.get("//order/status")).toMatchObject({ type: "string" });
  });

  it("indexes element names only, carrying no document text", () => {
    const tree = buildXmlPathTree([
      '<order id="7"><status>shipped</status></order>',
    ]);

    expect([...tree.keys()].sort()).toEqual([
      "//order",
      "//order/@id",
      "//order/status",
    ]);
    for (const info of tree.values()) {
      expect(Object.keys(info)).toEqual(["type"]);
    }
  });

  it("indexes attributes as @name paths, always as strings", () => {
    const tree = buildXmlPathTree(['<order id="7"><status>shipped</status></order>']);

    expect(tree.get("//order/@id")).toMatchObject({ type: "string" });
  });

  it("collapses repeated sibling elements onto the same path (no [*] needed)", () => {
    const tree = buildXmlPathTree([
      '<items><item sku="A1"/><item sku="B2"/><item sku="C3"/></items>',
    ]);

    expect(tree.has("//items/item[0]")).toBe(false);
    // <item/> has no child elements of its own (only attributes), so it's
    // indexed as a leaf — same classification rule as a plain text element.
    const item = tree.get("//items/item");
    expect(item?.type).toBe("string");
    expect(tree.get("//items/item/@sku")?.type).toBe("string");
  });

  it("unions paths across multiple samples", () => {
    const tree = buildXmlPathTree([
      "<order><a>1</a><b>x</b></order>",
      "<order><a>2</a></order>",
      "<order><a>3</a><b>y</b></order>",
    ]);

    expect([...tree.keys()].sort()).toEqual([
      "//order",
      "//order/a",
      "//order/b",
    ]);
  });

  it(`caps depth at ${MAX_DEPTH} levels`, () => {
    let inner = "<leaf>1</leaf>";
    for (let i = 0; i < MAX_DEPTH + 10; i++) inner = `<x>${inner}</x>`;
    const xml = `<root>${inner}</root>`;

    const tree = buildXmlPathTree([xml]);

    const longest = [...tree.keys()].reduce((a, b) => (a.length > b.length ? a : b));
    const depth = (longest.match(/\/x\b/g) || []).length;
    expect(depth).toBeLessThanOrEqual(MAX_DEPTH);
  });

  it("ignores malformed XML samples", () => {
    expect(buildXmlPathTree(["<order><status>shipped</status>"]).size).toBe(0);
  });

  it("ignores non-XML samples", () => {
    expect(buildXmlPathTree(["{}", "plain text", ""]).size).toBe(0);
  });

  it("skips only the malformed sample, keeping suggestions from valid ones", () => {
    const tree = buildXmlPathTree([
      "<order><status>shipped</status>",
      "<order><status>pending</status></order>",
    ]);

    expect(tree.get("//order/status")?.type).toBe("string");
  });

  it("does not index the browser's injected <parsererror> subtree", () => {
    const tree = buildXmlPathTree(["<order><status>shipped</status>"]);

    expect([...tree.keys()].some((k) => k.includes("parsererror"))).toBe(false);
  });

  it("skips xmlns declarations, which XPath 1.0 cannot address as attributes", () => {
    const tree = buildXmlPathTree([
      '<ns:order xmlns:ns="urn:x" xmlns="urn:d" ns:id="7"><ns:status>shipped</ns:status></ns:order>',
    ]);

    expect(tree.has("//ns:order/@xmlns")).toBe(false);
    expect(tree.has("//ns:order/@xmlns:ns")).toBe(false);
    // Prefixes are kept verbatim: that is what the backend's xmlquery matches.
    expect(tree.has("//ns:order/@ns:id")).toBe(true);
    expect(tree.has("//ns:order/ns:status")).toBe(true);
  });

  it("indexes an empty element as a path, so it can still be picked", () => {
    const tree = buildXmlPathTree(["<order><status></status><note/></order>"]);

    expect(tree.get("//order/status")?.type).toBe("string");
    expect(tree.get("//order/note")?.type).toBe("string");
  });

  it("lets container win when a path is a leaf in one sample and a parent in another", () => {
    const tree = buildXmlPathTree([
      "<order><note>hi</note></order>",
      "<order><note><b>hi</b></note></order>",
    ]);

    expect(tree.get("//order/note")?.type).toBe("object");
    expect(tree.has("//order/note/b")).toBe(true);
  });

  it(`caps the tree at ${MAX_PATHS} paths`, () => {
    const children = Array.from({ length: MAX_PATHS + 50 }, (_, i) => `<f${i}>v</f${i}>`).join("");

    const tree = buildXmlPathTree([`<root>${children}</root>`]);

    expect(tree.size).toBe(MAX_PATHS);
  });

  it("skips parsing further samples once the cap is reached", () => {
    // DOMParser on a hydrated multi-megabyte value dominates the cost here
    // and cannot be aborted midway, so the only saving available is not
    // starting it at all. Asserting on the resulting tree would prove
    // nothing — recordNode rejects surplus paths either way — so count the
    // parses instead.
    const children = Array.from(
      { length: MAX_PATHS + 50 },
      (_, i) => `<f${i}>v</f${i}>`,
    ).join("");

    const Original = globalThis.DOMParser;
    let parses = 0;
    class Counting extends Original {
      parseFromString(source: string, type: DOMParserSupportedType): Document {
        parses += 1;
        return super.parseFromString(source, type);
      }
    }
    globalThis.DOMParser = Counting as typeof DOMParser;
    try {
      const tree = buildXmlPathTree([
        `<root>${children}</root>`,
        "<second><marker>x</marker></second>",
        "<third><marker>y</marker></third>",
      ]);

      expect(tree.size).toBe(MAX_PATHS);
      expect(parses).toBe(1);
    } finally {
      globalThis.DOMParser = Original;
    }
  });

  it("walks every sample when the cap is not reached", () => {
    const tree = buildXmlPathTree(["<a><x>1</x></a>", "<b><y>2</y></b>"]);

    expect(tree.has("//a/x")).toBe(true);
    expect(tree.has("//b/y")).toBe(true);
  });
});
