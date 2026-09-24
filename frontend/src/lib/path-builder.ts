export type Token =
  | { kind: "key"; name: string }
  | { kind: "index"; value: number }
  | { kind: "star" };

const SAFE_KEY = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

export function buildJsonPath(trail: Token[]): string {
  let out = "$";
  for (const t of trail) {
    if (t.kind === "index") {
      out += `[${t.value}]`;
    } else if (t.kind === "star") {
      out += `[*]`;
    } else if (SAFE_KEY.test(t.name)) {
      out += `.${t.name}`;
    } else {
      const escaped = t.name.replace(/\\/g, "\\\\").replace(/'/g, "\\'");
      out += `['${escaped}']`;
    }
  }
  return out;
}

/**
 * Replaces every concrete array index in a trail with a wildcard.
 *
 * Arrays vary in length and order between messages, so a path pinned to a
 * fixed index (`$.items[1].price`) almost never matches the next record.
 * Clicking a value therefore always searches across every entry
 * (`$.items[*].price`) rather than asking the user to pick a scope.
 * Trails without any index are returned unchanged.
 */
export function wildcardArrayIndices(trail: Token[]): Token[] {
  return trail.map((t) => (t.kind === "index" ? { kind: "star" } : t));
}
