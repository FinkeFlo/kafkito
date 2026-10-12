import { describe, expect, it } from "vitest";
import { canonicalXml, canonicalXmlElement } from "./canonical-xml";

const same = (a: string, b: string, opts = {}) => {
  const ca = canonicalXml(a, opts);
  expect(ca).not.toBeNull();
  return ca === canonicalXml(b, opts);
};

describe("canonicalXml", () => {
  it("ignores attribute order and insignificant whitespace", () => {
    expect(
      same(
        '<order id="1" status="new"><item sku="a">2</item></order>',
        '<order  status="new"\n id="1">\n  <item sku="a"> 2 </item>\n</order>',
      ),
    ).toBe(true);
  });

  it("pretty-prints the canonical form", () => {
    expect(canonicalXml('<order b="2" a="1"><item>x</item><empty/></order>')).toBe(
      ['<order a="1" b="2">', "  <item>x</item>", "  <empty/>", "</order>"].join("\n"),
    );
  });

  it("ignores comments, processing instructions and the XML declaration", () => {
    expect(
      same(
        '<?xml version="1.0"?><order><!-- note --><?pi x?><id>1</id></order>',
        "<order><id>1</id></order>",
      ),
    ).toBe(true);
  });

  // Built by hand: happy-dom neither parses CDATA sections nor creates
  // them, browsers do. A text node posing as one stands in.
  it("treats CDATA like text", () => {
    const doc = new DOMParser().parseFromString("<a/>", "text/xml");
    const cdata = doc.createTextNode("< y");
    Object.defineProperty(cdata, "nodeType", { value: 4 /* CDATA_SECTION_NODE */ });
    doc.documentElement.append(doc.createTextNode("x "), cdata);
    expect(canonicalXmlElement(doc.documentElement)).toBe(canonicalXml("<a>x &lt; y</a>"));
  });

  it("keeps element order unless told otherwise", () => {
    const a = "<list><i>1</i><i>2</i></list>";
    const b = "<list><i>2</i><i>1</i></list>";
    expect(same(a, b)).toBe(false);
    expect(same(a, b, { ignoreChildOrder: true })).toBe(true);
  });

  it("finds real differences", () => {
    expect(same('<a x="1"/>', '<a x="2"/>')).toBe(false);
    expect(same("<a>1</a>", "<a>2</a>")).toBe(false);
    expect(same("<a><b/></a>", "<a><c/></a>")).toBe(false);
  });

  it("compares namespaces by URI, not by prefix", () => {
    expect(
      same(
        '<p:order xmlns:p="urn:shop"><p:id>1</p:id></p:order>',
        '<order xmlns="urn:shop"><id>1</id></order>',
      ),
    ).toBe(true);
    expect(same('<order xmlns="urn:shop"/>', '<order xmlns="urn:other"/>')).toBe(false);
  });

  it("returns null for anything that is not well-formed XML", () => {
    for (const bad of ["", "{}", "plain", "<a>", "<a></b>"]) {
      expect(canonicalXml(bad), bad).toBeNull();
    }
  });
});
