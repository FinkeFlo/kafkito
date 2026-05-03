// Private cluster configurations stored in the user's browser
// (localStorage). The server is stateless for private clusters — every
// request that targets one carries its configuration in the
// X-Kafkito-Cluster header. See docs/plans/per-user-clusters.md and the
// Go side (internal/server/private_cluster.go, pkg/kafka/adhoc.go).
//
// Security note: passwords are stored in cleartext in localStorage. This is
// documented in the UI. A future Webcrypto-based passphrase layer can wrap
// the value transparently without changing the storage schema.

export const PRIVATE_CLUSTER_SENTINEL = "__private__";

const STORAGE_KEY = "kafkito.private-clusters.v1";

export interface PrivateClusterAuth {
  type: "none" | "plain" | "scram-sha-256" | "scram-sha-512";
  username?: string;
  password?: string;
}

export interface PrivateClusterTLS {
  enabled: boolean;
  insecure_skip_verify?: boolean;
}

export interface PrivateClusterSchemaRegistry {
  url?: string;
  username?: string;
  password?: string;
  insecure_skip_verify?: boolean;
}

export interface PrivateCluster {
  id: string;
  name: string;
  brokers: string[];
  auth: PrivateClusterAuth;
  tls: PrivateClusterTLS;
  schema_registry?: PrivateClusterSchemaRegistry;
  created_at: number;
  updated_at: number;
}

function safeRead(): PrivateCluster[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(isPrivateCluster);
  } catch {
    return [];
  }
}

function safeWrite(items: PrivateCluster[]): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(items));
    window.dispatchEvent(new CustomEvent("kafkito:private-clusters-changed"));
  } catch {
    /* quota or access denied — ignore */
  }
}

function isPrivateCluster(v: unknown): v is PrivateCluster {
  if (!v || typeof v !== "object") return false;
  const o = v as Record<string, unknown>;
  return (
    typeof o.id === "string" &&
    typeof o.name === "string" &&
    Array.isArray(o.brokers) &&
    o.brokers.every((b) => typeof b === "string") &&
    typeof o.auth === "object" &&
    o.auth !== null
  );
}

export function listPrivateClusters(): PrivateCluster[] {
  return safeRead();
}

export function getPrivateClusterByName(name: string): PrivateCluster | null {
  return safeRead().find((c) => c.name === name) ?? null;
}

export function getPrivateClusterById(id: string): PrivateCluster | null {
  return safeRead().find((c) => c.id === id) ?? null;
}

export function upsertPrivateCluster(
  input: Omit<PrivateCluster, "id" | "created_at" | "updated_at"> & { id?: string },
): PrivateCluster {
  const items = safeRead();
  const now = Date.now();
  if (input.id) {
    const idx = items.findIndex((c) => c.id === input.id);
    if (idx >= 0) {
      const updated: PrivateCluster = {
        ...items[idx],
        ...input,
        id: items[idx].id,
        created_at: items[idx].created_at,
        updated_at: now,
      };
      items[idx] = updated;
      safeWrite(items);
      return updated;
    }
  }
  const created: PrivateCluster = {
    ...input,
    id: input.id ?? newId(),
    created_at: now,
    updated_at: now,
  };
  items.push(created);
  safeWrite(items);
  return created;
}

export function deletePrivateCluster(id: string): void {
  const items = safeRead().filter((c) => c.id !== id);
  safeWrite(items);
}

function newId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return "pc_" + Math.random().toString(36).slice(2, 12);
}

// --- Export / Import ---------------------------------------------------

interface ExportBundle {
  schema: "kafkito.private-clusters/v1";
  exported_at: string;
  clusters: PrivateCluster[];
}

/**
 * Returns an export bundle. When `ids` is provided, only clusters whose
 * id is in the set are included; otherwise every cluster is exported.
 * The bundle format is unchanged so partial exports stay round-trip
 * compatible with full exports.
 */
export function exportBundle(ids?: ReadonlySet<string>): ExportBundle {
  const all = safeRead();
  const clusters = ids ? all.filter((c) => ids.has(c.id)) : all;
  return {
    schema: "kafkito.private-clusters/v1",
    // allow-raw-date: serialization into a portable JSON export, not UI display
    exported_at: new Date().toISOString(),
    clusters,
  };
}

export interface ImportResult {
  added: number;
  updated: number;
  skipped: number;
}

export function importBundle(raw: string): ImportResult {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error("invalid JSON");
  }
  if (!parsed || typeof parsed !== "object") {
    throw new Error("invalid bundle");
  }
  const b = parsed as Partial<ExportBundle>;
  if (b.schema !== "kafkito.private-clusters/v1" || !Array.isArray(b.clusters)) {
    throw new Error("unsupported bundle format");
  }
  const existing = safeRead();
  const byId = new Map(existing.map((c) => [c.id, c]));
  let added = 0;
  let updated = 0;
  let skipped = 0;
  for (const item of b.clusters) {
    if (!isPrivateCluster(item)) {
      skipped++;
      continue;
    }
    if (byId.has(item.id)) {
      byId.set(item.id, { ...item, updated_at: Date.now() });
      updated++;
    } else {
      byId.set(item.id, item);
      added++;
    }
  }
  safeWrite(Array.from(byId.values()));
  return { added, updated, skipped };
}

// --- Backend header encoding ------------------------------------------

interface BackendClusterConfig {
  name: string;
  brokers: string[];
  auth: { type: string; username?: string; password?: string };
  tls: { enabled: boolean; insecure_skip_verify?: boolean };
  schema_registry?: {
    url?: string;
    username?: string;
    password?: string;
    insecure_skip_verify?: boolean;
  };
}

function toBackendConfig(c: PrivateCluster): BackendClusterConfig {
  return {
    name: c.name,
    brokers: c.brokers,
    auth: {
      type: c.auth.type,
      username: c.auth.username,
      password: c.auth.password,
    },
    tls: {
      enabled: c.tls.enabled,
      insecure_skip_verify: c.tls.insecure_skip_verify,
    },
    schema_registry: c.schema_registry
      ? {
          url: c.schema_registry.url,
          username: c.schema_registry.username,
          password: c.schema_registry.password,
          insecure_skip_verify: c.schema_registry.insecure_skip_verify,
        }
      : undefined,
  };
}

/** Returns the base64-encoded JSON payload for the X-Kafkito-Cluster header. */
export function encodePrivateClusterHeader(c: PrivateCluster): string {
  const json = JSON.stringify(toBackendConfig(c));
  return btoa(unescape(encodeURIComponent(json)));
}

/** Returns the raw backend-shaped ClusterConfig (used by the /clusters/_test endpoint body). */
export function toBackendClusterConfig(c: PrivateCluster): BackendClusterConfig {
  return toBackendConfig(c);
}

/** Subscribe to changes (from other tabs or in-tab edits). */
export function subscribePrivateClusters(cb: () => void): () => void {
  if (typeof window === "undefined") return () => undefined;
  const onStorage = (e: StorageEvent) => {
    if (e.key === STORAGE_KEY) cb();
  };
  const onCustom = () => cb();
  window.addEventListener("storage", onStorage);
  window.addEventListener("kafkito:private-clusters-changed", onCustom);
  return () => {
    window.removeEventListener("storage", onStorage);
    window.removeEventListener("kafkito:private-clusters-changed", onCustom);
  };
}
