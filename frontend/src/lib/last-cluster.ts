const KEY = "kafkito.lastClusterId";

export function getLastCluster(): string | null {
  try {
    return localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export function setLastCluster(name: string): void {
  try {
    localStorage.setItem(KEY, name);
  } catch {
    // Storage unavailable (private mode, quota): silently no-op.
  }
}

export function clearLastCluster(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // Storage unavailable: silently no-op.
  }
}
