// Private cluster configurations stored in the user's browser
// (localStorage). The server is stateless for private clusters — every
// request that targets one carries its configuration in the
// X-Kafkito-Cluster header. The Go side lives in
// internal/server/private_cluster.go and internal/kafka/adhoc.go.
//
// Passwords: a cluster saved with remember_credentials false (the form's
// default) keeps its SASL and Schema Registry passwords in this module only,
// for the life of the tab; localStorage gets the definition without them. A
// new tab asks for them again (missingPasswords, setSessionPasswords). A
// held password is dropped once the cluster's connection settings change.
// Exports leave such passwords out.
// Remembered passwords, and those of entries saved before the setting
// existed, are stored in cleartext in localStorage; the UI says so. Export
// files are encrypted with a user passphrase
// (private-clusters-export-crypto.ts wraps the exportBundle() JSON).

import type { components } from "./api.gen";

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
  /**
   * Set on clusters that do not remember their passwords: whether the
   * registry has a password a tab has to ask for. A registry user without
   * a password is valid.
   */
  credential_required?: boolean;
}

export interface PrivateCluster {
  id: string;
  name: string;
  is_prod?: boolean;
  brokers: string[];
  auth: PrivateClusterAuth;
  tls: PrivateClusterTLS;
  schema_registry?: PrivateClusterSchemaRegistry;
  /**
   * Whether the passwords are stored in localStorage. Only false keeps them
   * out; entries saved before the setting existed have no value and keep
   * their stored passwords.
   */
  remember_credentials?: boolean;
  created_at: number;
  updated_at: number;
}

interface SessionPasswords {
  password?: string;
  srPassword?: string;
}

// A held password belongs to the connection it was entered for: the key of
// the settings it goes to. A definition whose brokers, auth, TLS or Schema
// Registry settings changed since (in another tab, by an import) does not
// get it, so a password is never sent somewhere else than it was meant for.
interface HeldPasswords {
  password?: string;
  saslKey: string;
  srPassword?: string;
  srKey: string;
}

function saslKey(c: PrivateCluster): string {
  return JSON.stringify([
    c.brokers,
    c.auth.type,
    c.auth.username ?? "",
    !!c.tls?.enabled,
    !!c.tls?.insecure_skip_verify,
  ]);
}

function srKey(c: PrivateCluster): string {
  const sr = c.schema_registry;
  return JSON.stringify([sr?.url ?? "", sr?.username ?? "", !!sr?.insecure_skip_verify]);
}

/** The passwords of clusters that do not remember them, by id, for this tab. */
const sessionPasswords = new Map<string, HeldPasswords>();

function hold(c: PrivateCluster, pw: SessionPasswords): void {
  sessionPasswords.set(c.id, {
    password: pw.password,
    saslKey: saslKey(c),
    srPassword: pw.srPassword,
    srKey: srKey(c),
  });
}

/** Returns the held passwords that still belong to c's settings. */
function heldFor(c: PrivateCluster, h: HeldPasswords): SessionPasswords {
  return {
    password: h.saslKey === saslKey(c) ? h.password : undefined,
    srPassword: h.srKey === srKey(c) ? h.srPassword : undefined,
  };
}

function remembersCredentials(c: PrivateCluster): boolean {
  return c.remember_credentials !== false;
}

/**
 * Returns c as it is stored. For a cluster that does not remember its
 * passwords, the passwords it carries move to the session map.
 */
function toStored(c: PrivateCluster): PrivateCluster {
  if (remembersCredentials(c)) {
    sessionPasswords.delete(c.id);
    return c;
  }
  // c carries every password this tab has for it (readAll merges them in,
  // a form save brings its own), so what it lacks is not held any more.
  const pw: SessionPasswords = {
    password: c.auth.password || undefined,
    srPassword: c.schema_registry?.password || undefined,
  };
  if (pw.password !== undefined || pw.srPassword !== undefined) hold(c, pw);
  else sessionPasswords.delete(c.id);
  const sr = c.schema_registry;
  return {
    ...c,
    auth: { ...c.auth, password: undefined },
    schema_registry: sr
      ? {
          ...sr,
          password: undefined,
          // A tab without the password keeps what an earlier save recorded.
          credential_required: pw.srPassword !== undefined || (sr.credential_required ?? false),
        }
      : undefined,
  };
}

/** Returns a stored cluster with the passwords this tab holds for it. */
function withSessionPasswords(c: PrivateCluster): PrivateCluster {
  const h = remembersCredentials(c) ? undefined : sessionPasswords.get(c.id);
  if (!h) return c;
  const pw = heldFor(c, h);
  return {
    ...c,
    auth: { ...c.auth, password: c.auth.password ?? pw.password },
    schema_registry: c.schema_registry
      ? { ...c.schema_registry, password: c.schema_registry.password ?? pw.srPassword }
      : undefined,
  };
}

/**
 * Reports which passwords a cluster that does not remember them lacks in
 * this tab: the SASL password when it uses SASL, the Schema Registry
 * password when it was saved with one. A remembered cluster lacks none; it
 * is sent as stored, and the server reports what is missing.
 */
export function missingPasswords(c: PrivateCluster): { password: boolean; srPassword: boolean } {
  if (remembersCredentials(c)) return { password: false, srPassword: false };
  const sr = c.schema_registry;
  return {
    password: c.auth.type !== "none" && !c.auth.password,
    srPassword: !!sr?.url && !!sr.credential_required && !sr.password,
  };
}

/** Whether c lacks a password it needs (see missingPasswords). */
export function needsPasswords(c: PrivateCluster): boolean {
  const m = missingPasswords(c);
  return m.password || m.srPassword;
}

/** Keeps the passwords the user entered for cluster id, in this tab only. */
export function setSessionPasswords(id: string, pw: SessionPasswords): void {
  const c = safeRead().find((x) => x.id === id);
  if (!c) return;
  const prev = sessionPasswords.get(id);
  const held = prev ? heldFor(c, prev) : {};
  hold(c, {
    password: pw.password ?? held.password,
    srPassword: pw.srPassword ?? held.srPassword,
  });
  notifyChanged();
}

/**
 * Thrown instead of sending a request for a private cluster whose
 * passwords this tab does not have. The cluster layout asks for them.
 */
export class PasswordRequiredError extends Error {
  constructor(readonly clusterName: string) {
    super(`Enter the password of "${clusterName}" to use it in this tab.`);
    this.name = "PasswordRequiredError";
  }
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
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(items.map(toStored)));
    notifyChanged();
  } catch {
    /* quota or access denied — ignore */
  }
}

function notifyChanged(): void {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent("kafkito:private-clusters-changed"));
}

function isPrivateCluster(v: unknown): v is PrivateCluster {
  if (!v || typeof v !== "object") return false;
  const o = v as Record<string, unknown>;
  return (
    typeof o.id === "string" &&
    typeof o.name === "string" &&
    (o.is_prod === undefined || typeof o.is_prod === "boolean") &&
    (o.remember_credentials === undefined || typeof o.remember_credentials === "boolean") &&
    Array.isArray(o.brokers) &&
    o.brokers.every((b) => typeof b === "string") &&
    typeof o.auth === "object" &&
    o.auth !== null
  );
}

// The readers return each cluster with the passwords this tab holds for it.
function readAll(): PrivateCluster[] {
  return safeRead().map(withSessionPasswords);
}

export function listPrivateClusters(): PrivateCluster[] {
  return readAll();
}

export function getPrivateClusterByName(name: string): PrivateCluster | null {
  return readAll().find((c) => c.name === name) ?? null;
}

export function getPrivateClusterById(id: string): PrivateCluster | null {
  return readAll().find((c) => c.id === id) ?? null;
}

export function upsertPrivateCluster(
  input: Omit<PrivateCluster, "id" | "created_at" | "updated_at"> & { id?: string },
): PrivateCluster {
  const items = readAll();
  const now = Date.now();
  if (input.id) {
    const idx = items.findIndex((c) => c.id === input.id);
    if (idx >= 0) {
      const updated: PrivateCluster = {
        ...items[idx],
        ...input,
        // An input without a value keeps the setting, so an update can
        // never move passwords into localStorage by leaving it out.
        remember_credentials: input.remember_credentials ?? items[idx].remember_credentials,
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
  const items = readAll().filter((c) => c.id !== id);
  sessionPasswords.delete(id);
  safeWrite(items);
}

function newId(): string {
  const c = typeof globalThis !== "undefined" ? globalThis.crypto : undefined;
  if (c?.randomUUID) {
    return c.randomUUID();
  }
  if (c?.getRandomValues) {
    const bytes = new Uint8Array(8);
    c.getRandomValues(bytes);
    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
    return "pc_" + hex;
  }
  return "pc_" + Date.now().toString(36);
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
  // As stored: a cluster that does not remember its passwords is exported
  // without them, also when this tab holds them.
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
  const existing = readAll();
  const byId = new Map(existing.map((c) => [c.id, c]));
  let added = 0;
  let updated = 0;
  let skipped = 0;
  for (const item of b.clusters) {
    if (!isPrivateCluster(item)) {
      skipped++;
      continue;
    }
    const prev = byId.get(item.id);
    if (prev) {
      // A file without the setting (written before it existed) keeps the
      // entry's, so it cannot move passwords into localStorage.
      byId.set(item.id, {
        ...item,
        remember_credentials: item.remember_credentials ?? prev.remember_credentials,
        updated_at: Date.now(),
      });
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

type BackendClusterConfig = components["schemas"]["ClusterConfig"];

function toBackendConfig(c: PrivateCluster): BackendClusterConfig {
  return {
    name: c.name,
    is_prod: c.is_prod,
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

/**
 * Returns the base64-encoded JSON payload for the X-Kafkito-Cluster header.
 * Throws PasswordRequiredError when c lacks a password it needs.
 */
export function encodePrivateClusterHeader(c: PrivateCluster): string {
  if (needsPasswords(c)) throw new PasswordRequiredError(c.name);
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
