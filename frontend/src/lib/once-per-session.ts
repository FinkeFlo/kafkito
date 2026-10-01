const PREFIX = "kafkito.once.";

// Fallback for when sessionStorage throws (blocked site data, some private
// modes): the claim then lasts until the page is reloaded.
const claimed = new Set<string>();

/**
 * Returns true the first time it is called for `key` in this browser tab's
 * session, false afterwards. Use it for one-off hints such as a toast that
 * should not come back on every navigation.
 */
export function claimOncePerSession(key: string): boolean {
  if (claimed.has(key)) return false;
  claimed.add(key);
  try {
    if (sessionStorage.getItem(PREFIX + key)) return false;
    sessionStorage.setItem(PREFIX + key, "1");
  } catch {
    // Storage unavailable; the in-memory set above already records the claim.
  }
  return true;
}
