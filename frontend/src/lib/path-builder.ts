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
