// latestVersion returns the highest schema version, independent of the order
// the registry returned them in. Defaults to 1 when no versions are known.
export function latestVersion(versions: number[]): number {
  return versions.length > 0 ? Math.max(...versions) : 1;
}

// searchParamString reads a Schemas page search param as a string. The
// router parses search values as JSON, so `?version=1` arrives as the number
// 1, not the string "1"; a string-only check would drop it and fall back to
// the latest version.
export function searchParamString(v: unknown): string | undefined {
  if (typeof v === "string") return v;
  if (typeof v === "number" && Number.isFinite(v)) return String(v);
  return undefined;
}
