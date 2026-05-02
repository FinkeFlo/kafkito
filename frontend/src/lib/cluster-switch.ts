/**
 * Cluster-switch URL semantics — when the user changes the active cluster
 * from a per-cluster route, preserve the section (segments[2]) but drop any
 * deeper context (resource ID at segments[3]+). The new cluster may not have
 * the same resource by ID; landing on the section index is the safe default.
 *
 * Special case: under `/security`, segments[3] is itself a sub-tab name
 * (`acls` or `users`), not a resource ID, so it is also preserved. Anything
 * deeper than the sub-tab is still dropped.
 *
 * Examples:
 *   /clusters/A                       → /clusters/B                      (default landing per route 2.2 redirect)
 *   /clusters/A/topics                → /clusters/B/topics               (preserve)
 *   /clusters/A/topics/foo            → /clusters/B/topics               (drop foo)
 *   /clusters/A/topics/foo/messages   → /clusters/B/topics               (drop foo + tab)
 *   /clusters/A/groups/X              → /clusters/B/groups               (drop X)
 *   /clusters/A/security              → /clusters/B/security             (preserve, will redirect to /security/acls)
 *   /clusters/A/security/acls         → /clusters/B/security/acls        (preserve sub-tab)
 *   /clusters/A/security/users/Y      → /clusters/B/security/users       (preserve sub-tab, drop Y)
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
  if (!section) return `/clusters/${encoded}`;
  if (section === "security" && segments[3]) {
    // Preserve the security sub-tab (acls / users) so cluster-switch from
    // a deep security URL doesn't kick the user back to the ACLs default.
    return `/clusters/${encoded}/security/${segments[3]}`;
  }
  return `/clusters/${encoded}/${section}`;
}
