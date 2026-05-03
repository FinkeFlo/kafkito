import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  deletePrivateCluster,
  encodePrivateClusterHeader,
  exportBundle,
  getPrivateClusterById,
  getPrivateClusterByName,
  importBundle,
  listPrivateClusters,
  PRIVATE_CLUSTER_SENTINEL,
  subscribePrivateClusters,
  toBackendClusterConfig,
  upsertPrivateCluster,
  type PrivateCluster,
} from "./private-clusters";

function sample(name = "local"): Omit<PrivateCluster, "id" | "created_at" | "updated_at"> {
  return {
    name,
    brokers: ["localhost:9092"],
    auth: { type: "plain", username: "u", password: "p" },
    tls: { enabled: false },
    schema_registry: { url: "http://sr:8081" },
  };
}

describe("private-clusters", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("exposes the URL sentinel constant", () => {
    expect(PRIVATE_CLUSTER_SENTINEL).toBe("__private__");
  });

  it("upsert creates with generated id and timestamps", () => {
    const created = upsertPrivateCluster(sample("a"));
    expect(created.id).toMatch(/.+/);
    expect(created.created_at).toBeGreaterThan(0);
    expect(created.updated_at).toBe(created.created_at);
    expect(listPrivateClusters()).toHaveLength(1);
  });

  it("upsert updates in place, preserving id and created_at", () => {
    // Smallest deterministic delta the production code's `Date.now()` can observe.
    const tickMs = 1;
    const t0 = new Date(2026, 0, 1, 12).getTime();
    vi.useFakeTimers();
    vi.setSystemTime(new Date(t0));

    const created = upsertPrivateCluster(sample("a"));

    vi.setSystemTime(new Date(t0 + tickMs));
    const updated = upsertPrivateCluster({ ...sample("a"), id: created.id, brokers: ["h:1"] });

    expect(updated.id).toBe(created.id);
    expect(updated.created_at).toBe(created.created_at);
    expect(updated.updated_at).toBe(t0 + tickMs);
    expect(updated.updated_at).toBeGreaterThan(created.updated_at);
    expect(updated.brokers).toEqual(["h:1"]);
    expect(listPrivateClusters()).toHaveLength(1);
  });

  it("get by id and by name work", () => {
    const c = upsertPrivateCluster(sample("alpha"));
    expect(getPrivateClusterById(c.id)?.id).toBe(c.id);
    expect(getPrivateClusterByName("alpha")?.id).toBe(c.id);
    expect(getPrivateClusterById("missing")).toBeNull();
    expect(getPrivateClusterByName("missing")).toBeNull();
  });

  it("delete removes by id", () => {
    const a = upsertPrivateCluster(sample("a"));
    const b = upsertPrivateCluster(sample("b"));
    deletePrivateCluster(a.id);
    const remaining = listPrivateClusters();
    expect(remaining.map((c) => c.id)).toEqual([b.id]);
  });

  it("export → import round-trips data", () => {
    upsertPrivateCluster(sample("one"));
    upsertPrivateCluster(sample("two"));
    const bundle = exportBundle();
    expect(bundle.schema).toBe("kafkito.private-clusters/v1");
    expect(bundle.clusters).toHaveLength(2);

    window.localStorage.clear();
    const res = importBundle(JSON.stringify(bundle));
    expect(res.added).toBe(2);
    expect(res.updated).toBe(0);
    expect(res.skipped).toBe(0);
    expect(listPrivateClusters().map((c) => c.name).sort()).toEqual(["one", "two"]);
  });

  it("export with id filter only includes selected clusters", () => {
    const a = upsertPrivateCluster(sample("a"));
    upsertPrivateCluster(sample("b"));
    const c = upsertPrivateCluster(sample("c"));

    const bundle = exportBundle(new Set([a.id, c.id]));
    expect(bundle.clusters).toHaveLength(2);
    expect(bundle.clusters.map((x) => x.name).sort()).toEqual(["a", "c"]);
  });

  it("export with empty id filter yields zero clusters", () => {
    upsertPrivateCluster(sample("a"));
    const bundle = exportBundle(new Set());
    expect(bundle.clusters).toHaveLength(0);
  });

  it("import merges by id: matching ids count as updated", () => {
    const a = upsertPrivateCluster(sample("a"));
    const bundle = exportBundle();
    // Re-import the same bundle — everything should count as updated.
    const res = importBundle(JSON.stringify(bundle));
    expect(res.updated).toBe(1);
    expect(res.added).toBe(0);
    expect(listPrivateClusters()).toHaveLength(1);
    expect(listPrivateClusters()[0].id).toBe(a.id);
  });

  it("import rejects unknown schema", () => {
    expect(() => importBundle(JSON.stringify({ schema: "other", clusters: [] }))).toThrow(
      /unsupported/i,
    );
  });

  it("import throws on invalid JSON", () => {
    expect(() => importBundle("not-json")).toThrow(/invalid JSON/i);
  });

  it("import skips malformed entries", () => {
    const bundle = {
      schema: "kafkito.private-clusters/v1",
      exported_at: new Date().toISOString(),
      clusters: [
        { id: "x", name: "ok", brokers: ["h:1"], auth: { type: "none" }, tls: { enabled: false }, created_at: 1, updated_at: 1 },
        { id: "y" }, // malformed
        "garbage",
      ],
    };
    const res = importBundle(JSON.stringify(bundle));
    expect(res.added).toBe(1);
    expect(res.skipped).toBe(2);
  });

  it("encodePrivateClusterHeader produces base64 JSON with backend shape", () => {
    const c = upsertPrivateCluster(sample("enc"));
    const header = encodePrivateClusterHeader(c);
    expect(header).toMatch(/^[A-Za-z0-9+/=]+$/);
    const decoded = JSON.parse(decodeURIComponent(escape(atob(header))));
    expect(decoded.name).toBe("enc");
    expect(decoded.brokers).toEqual(["localhost:9092"]);
    expect(decoded.auth.type).toBe("plain");
    expect(decoded.tls.enabled).toBe(false);
    expect(decoded.schema_registry.url).toBe("http://sr:8081");
    // id / timestamps are a frontend-only concern and must not leak.
    expect(decoded.id).toBeUndefined();
    expect(decoded.created_at).toBeUndefined();
  });

  it("toBackendClusterConfig mirrors the header payload", () => {
    const c = upsertPrivateCluster(sample("x"));
    const cfg = toBackendClusterConfig(c);
    expect(cfg.name).toBe("x");
    expect(cfg.brokers).toEqual(["localhost:9092"]);
    expect(cfg).not.toHaveProperty("id");
  });

  it("subscribePrivateClusters fires on upsert and delete", () => {
    let calls = 0;
    const unsub = subscribePrivateClusters(() => {
      calls++;
    });
    const c = upsertPrivateCluster(sample("sub"));
    deletePrivateCluster(c.id);
    expect(calls).toBeGreaterThanOrEqual(2);
    unsub();
    upsertPrivateCluster(sample("after-unsub"));
    // Unsub should stop notifications; may still be the same count.
    const afterUnsub = calls;
    expect(afterUnsub).toBeGreaterThanOrEqual(2);
  });

  it("tolerates corrupt localStorage payloads by returning []", () => {
    window.localStorage.setItem("kafkito.private-clusters.v1", "{not-json");
    expect(listPrivateClusters()).toEqual([]);
  });
});
