// XML equivalent of path-tree.ts's buildPathTree, used to feed PathSense
// suggestions for XPath search mode. Reuses the same PathTree/PathInfo shape
// so <PathSense> needs no changes to support either mode.
import { MAX_DEPTH, MAX_PATHS, MAX_SAMPLE_VALUES, type PathTree } from "./path-tree";

/** Cheap pre-check mirroring the backend's own `trimmed[0] == '<'` XML guard
 * (decodeBytes/looksXML in pkg/kafka/consumer.go) — used to filter sample
 * values before attempting a full DOMParser parse. */
export function looksLikeXml(value: string | undefined | null): boolean {
  return !!value && value.trimStart().startsWith("<");
}

function elementChildren(el: Element): Element[] {
  const out: Element[] = [];
  for (const child of Array.from(el.childNodes)) {
    if (child.nodeType === 1 /* Node.ELEMENT_NODE */) out.push(child as Element);
  }
  return out;
}

// DOMParser reports XML parse failures by handing back an error document
// rather than by throwing, and browsers disagree on the shape: Firefox makes
// <parsererror> the root in its own namespace, while Blink/WebKit (and jsdom)
// splice an XHTML-namespaced <parsererror> into the partially parsed tree.
// Catching both matters because the partial tree is otherwise indexed as if
// it were real, polluting suggestions with paths like `//order/parsererror/h3`.
// Both checks are namespace/root anchored, so a legitimate document that
// merely contains an element named "parsererror" isn't discarded.
const PARSERERROR_NS = [
  "http://www.w3.org/1999/xhtml",
  "http://www.mozilla.org/newlayout/xml/parsererror.xml",
];

function hasParserError(doc: Document): boolean {
  const root = doc.documentElement;
  if (!root) return true;
  if (root.localName === "parsererror") return true;
  // getElementsByTagName rather than ...NS: the namespaced variant is not
  // reliably implemented across DOM engines (happy-dom throws on XML
  // documents), so match by name and check the namespace on the node.
  return Array.from(doc.getElementsByTagName("parsererror")).some((el) =>
    PARSERERROR_NS.includes(el.namespaceURI ?? ""),
  );
}

// Parses a raw XML string, returning null for anything that isn't XML or
// that fails to parse.
function parseXmlDoc(value: string): Document | null {
  if (!looksLikeXml(value)) return null;
  let doc: Document;
  try {
    doc = new DOMParser().parseFromString(value.trim(), "text/xml");
  } catch {
    return null;
  }
  return hasParserError(doc) ? null : doc;
}

// Namespace declarations (xmlns / xmlns:foo) are not addressable as
// attributes in XPath 1.0 — which is what the backend's xmlquery evaluates —
// so suggesting `//root/@xmlns` would hand the user a path that can never
// match.
function isNamespaceDeclaration(name: string): boolean {
  return name === "xmlns" || name.startsWith("xmlns:");
}

function recordNode(
  tree: PathTree,
  seenInThisSample: Set<string>,
  path: string,
  isContainer: boolean,
  textValue: string,
) {
  const firstTimeInSample = !seenInThisSample.has(path);
  seenInThisSample.add(path);

  // An empty element (`<item/>`, `<status></status>`) carries no value worth
  // offering an `= ""` filter for, so it records no sample value and
  // PathSense falls back to `exists` semantics when it's picked.
  const hasValue = !isContainer && textValue !== "";

  const existing = tree.get(path);
  if (!existing) {
    if (tree.size >= MAX_PATHS) return;
    tree.set(path, {
      type: isContainer ? "object" : "string",
      sampleValues: hasValue ? [textValue] : [],
      distinctCount: hasValue ? 1 : 0,
      fromN: 1,
    });
    return;
  }
  if (firstTimeInSample) {
    existing.fromN += 1;
  }
  // The same path can be a leaf in one sample and a parent in another
  // (`<note>hi</note>` vs `<note><b>hi</b></note>`). Container wins, so the
  // suggestion offers `exists` rather than an `=` against a value only some
  // documents have.
  if (isContainer) {
    existing.type = "object";
    existing.sampleValues.length = 0;
    existing.distinctCount = 0;
    return;
  }
  if (existing.type === "object" || !hasValue) return;
  if (!existing.sampleValues.includes(textValue)) {
    existing.distinctCount += 1;
    if (existing.sampleValues.length < MAX_SAMPLE_VALUES) {
      existing.sampleValues.push(textValue);
    }
  }
}

// Unlike JSON arrays, repeated sibling elements with the same tag name don't
// need a `[*]` normalization step: an XPath like `//items/item` already
// matches every `item` sibling, so they collapse onto the same path key
// naturally just by walking children and appending `/${tagName}`.
function walkElement(
  tree: PathTree,
  seenInThisSample: Set<string>,
  el: Element,
  path: string,
  depth: number,
) {
  if (depth > MAX_DEPTH) return;
  // See MAX_PATHS: once the cap is reached the rest of the document can only
  // produce paths that get discarded, so stop instead of walking it out.
  if (tree.size >= MAX_PATHS) return;

  const children = elementChildren(el);
  const isContainer = children.length > 0;
  const text = isContainer ? "" : (el.textContent ?? "").trim();
  recordNode(tree, seenInThisSample, path, isContainer, text);

  for (const attr of Array.from(el.attributes)) {
    if (isNamespaceDeclaration(attr.name)) continue;
    recordNode(tree, seenInThisSample, `${path}/@${attr.name}`, false, attr.value);
  }

  for (const child of children) {
    // tagName keeps any namespace prefix (`ns:order`), which is what the
    // backend's xmlquery matches against literally.
    walkElement(tree, seenInThisSample, child, `${path}/${child.tagName}`, depth + 1);
  }
}

export function buildXmlPathTree(samples: string[]): PathTree {
  const tree: PathTree = new Map();
  for (const sample of samples) {
    // Checked before parsing, not just before walking: DOMParser on a
    // hydrated multi-megabyte value is the dominant cost here, and a full
    // tree has no use for the result.
    if (tree.size >= MAX_PATHS) break;
    const doc = parseXmlDoc(sample);
    const root = doc?.documentElement;
    if (!root) continue;
    walkElement(tree, new Set<string>(), root, `//${root.tagName}`, 1);
  }
  return tree;
}
