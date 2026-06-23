// latestVersion returns the highest schema version, independent of the order
// the registry returned them in. Defaults to 1 when no versions are known.
export function latestVersion(versions: number[]): number {
  return versions.length > 0 ? Math.max(...versions) : 1;
}
