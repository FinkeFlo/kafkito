// Canonical form of an XML document for the semantic message compare:
// attributes sorted, namespace prefixes replaced by canonical ones derived
// from the namespace URIs, insignificant whitespace, comments and processing
// instructions gone, optionally sibling order ignored. Two documents are
// equivalent when their canonical forms are equal.
import { parseXmlDoc } from "./xml-path-tree";

export interface XmlCanonicalOptions {
  /** Compare siblings as a multiset: element order inside a parent is ignored. */
  ignoreChildOrder?: boolean;
}

const XMLNS_NS = "http://www.w3.org/2000/xmlns/";
const XML_NS = "http://www.w3.org/XML/1998/namespace";
const ELEMENT_NODE = 1;
const TEXT_NODE = 3;
const CDATA_SECTION_NODE = 4;
const MAX_NESTING = 512;

/**
 * Pretty-printed canonical form of `text`, or null when it is not a
 * well-formed XML document.
 */
export function canonicalXml(text: string, opts: XmlCanonicalOptions = {}): string | null {
  const root = parseXmlDoc(text)?.documentElement;
  return root ? canonicalXmlElement(root, opts) : null;
}

/** Canonical form of a parsed document's root element; null when nested too deeply. */
export function canonicalXmlElement(root: Element, opts: XmlCanonicalOptions = {}): string | null {
  // The prefix a document picks is irrelevant; the namespace URI is not.
  // Prefixes are assigned from the sorted URIs, so both sides map a URI to
  // the same prefix as long as they use the same set of namespaces.
  const uris = new Set<string>();
  collectNamespaces(root, uris, 0);
  const prefixes = new Map<string, string>();
  for (const [i, uri] of [...uris].sort().entries()) prefixes.set(uri, `ns${i + 1}`);
  const decls = [...prefixes].map(([uri, p]) => `xmlns:${p}="${escapeAttr(uri)}"`);
  try {
    return renderElement(root, "", { opts, prefixes }, 0, decls);
  } catch (e) {
    if (e instanceof RangeError) return null;
    throw e;
  }
}

interface Ctx {
  opts: XmlCanonicalOptions;
  prefixes: Map<string, string>;
}

function isNamespaceDeclaration(attr: Attr): boolean {
  return attr.namespaceURI === XMLNS_NS || attr.name === "xmlns" || attr.name.startsWith("xmlns:");
}

function collectNamespaces(el: Element, uris: Set<string>, depth: number) {
  if (depth > MAX_NESTING) throw new RangeError("XML nesting too deep");
  if (el.namespaceURI) uris.add(el.namespaceURI);
  for (const attr of Array.from(el.attributes)) {
    if (isNamespaceDeclaration(attr)) continue;
    if (attr.namespaceURI && attr.namespaceURI !== XML_NS) uris.add(attr.namespaceURI);
  }
  for (const child of Array.from(el.children)) collectNamespaces(child, uris, depth + 1);
}

function qualifiedName(node: Element | Attr, ctx: Ctx): string {
  const local = node.localName ?? node.nodeName;
  const uri = node.namespaceURI;
  if (!uri) return local;
  if (uri === XML_NS) return `xml:${local}`;
  return `${ctx.prefixes.get(uri)}:${local}`;
}

function renderElement(
  el: Element,
  indent: string,
  ctx: Ctx,
  depth: number,
  extraAttrs: string[] = [],
): string {
  if (depth > MAX_NESTING) throw new RangeError("XML nesting too deep");
  const attrs = Array.from(el.attributes)
    .filter((a) => !isNamespaceDeclaration(a))
    .map((a) => `${qualifiedName(a, ctx)}="${escapeAttr(a.value)}"`)
    .sort();
  const name = qualifiedName(el, ctx);
  const open = [name, ...attrs, ...extraAttrs].join(" ");

  const inner = `${indent}  `;
  const children: string[] = [];
  let text = "";
  const flushText = () => {
    const t = text.trim();
    if (t) children.push(inner + escapeText(t));
    text = "";
  };
  for (const child of Array.from(el.childNodes)) {
    if (child.nodeType === TEXT_NODE || child.nodeType === CDATA_SECTION_NODE) {
      // Adjacent text and CDATA sections are one piece of character data.
      text += child.nodeValue ?? "";
    } else if (child.nodeType === ELEMENT_NODE) {
      flushText();
      children.push(renderElement(child as Element, inner, ctx, depth + 1));
    }
    // Comments and processing instructions carry no data.
  }
  flushText();

  if (children.length === 0) return `${indent}<${open}/>`;
  if (children.length === 1 && !children[0].trimStart().startsWith("<")) {
    return `${indent}<${open}>${children[0].trimStart()}</${name}>`;
  }
  if (ctx.opts.ignoreChildOrder) children.sort();
  return `${indent}<${open}>\n${children.join("\n")}\n${indent}</${name}>`;
}

function escapeText(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttr(s: string): string {
  return escapeText(s)
    .replace(/"/g, "&quot;")
    .replace(/\n/g, "&#10;")
    .replace(/\t/g, "&#9;")
    .replace(/\r/g, "&#13;");
}
