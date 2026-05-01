/**
 * Build the URL to navigate to when the user picks a new cluster from the
 * cluster-switcher. Preserves the resource SECTION (topics / groups /
 * schemas / brokers / acls / dashboard) but drops item-level context and
 * query-string filter state.
 *
 * @param currentPath  the current `location.pathname` (no query string)
 * @param newClusterName the un-encoded display name of the target cluster
 * @returns the new path; URL-encoded, no query string
 */
export function computeSwitchTarget(
  currentPath: string,
  newClusterName: string,
): string {
  const segments = currentPath.split("/").filter(Boolean);
  // Expected shape: ["clusters", "<currentClusterEncoded>", "<section>", ...]
  // For non-cluster routes this function should not be called.
  const section = segments[2] ?? "";
  const encoded = encodeURIComponent(newClusterName);
  return section ? `/clusters/${encoded}/${section}` : `/clusters/${encoded}`;
}
