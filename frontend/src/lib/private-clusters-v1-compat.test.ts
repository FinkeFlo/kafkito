import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  encodePrivateClusterHeader,
  exportBundle,
  getPrivateClusterByName,
  importBundle,
  listPrivateClusters,
  upsertPrivateCluster,
} from "./private-clusters";

// Saved connections (including passwords) live under this key. Existing users
// lose them if the key or the stored shape changes, so the fixture is the
// literal string today's settings form writes: key order of toPrivateCluster()
// followed by created_at/updated_at, undefined fields dropped by
// JSON.stringify.
const STORAGE_KEY = "kafkito.private-clusters.v1";
const V1 =
  "[" +
  '{"id":"0b9f3c5e-2d4a-4f8e-9c1b-7a6d5e4f3a2b","name":"prod-eu","is_prod":true,' +
  '"brokers":["kafka-1.example.com:9093","kafka-2.example.com:9093"],' +
  '"auth":{"type":"scram-sha-512","username":"svc-kafkito","password":"s3cr3t/p%ss #1"},' +
  '"tls":{"enabled":true,"insecure_skip_verify":false},' +
  '"schema_registry":{"url":"https://sr.example.com","username":"sr-user","password":"sr-p@ss","insecure_skip_verify":true},' +
  '"created_at":1767261600000,"updated_at":1767348000000},' +
  '{"id":"pc_1a2b3c4d5e6f7a8b","name":"dev","is_prod":false,"brokers":["10.0.0.5:9092"],' +
  '"auth":{"type":"plain","username":"dev","password":"dev-pw"},' +
  '"tls":{"enabled":false,"insecure_skip_verify":false},' +
  '"created_at":1767261600000,"updated_at":1767261600000},' +
  '{"id":"pc_legacy","name":"legacy","brokers":["10.0.0.6:9092"],"auth":{"type":"none"},' +
  '"tls":{"enabled":false},"created_at":0,"updated_at":0}' +
  "]";

describe("private-cluster storage v1 compatibility", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.localStorage.setItem(STORAGE_KEY, V1);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("reads every field of a stored v1 payload", () => {
    expect(listPrivateClusters()).toEqual(JSON.parse(V1));
    const prod = getPrivateClusterByName("prod-eu");
    expect(prod).toEqual({
      id: "0b9f3c5e-2d4a-4f8e-9c1b-7a6d5e4f3a2b",
      name: "prod-eu",
      is_prod: true,
      brokers: ["kafka-1.example.com:9093", "kafka-2.example.com:9093"],
      auth: { type: "scram-sha-512", username: "svc-kafkito", password: "s3cr3t/p%ss #1" },
      tls: { enabled: true, insecure_skip_verify: false },
      schema_registry: {
        url: "https://sr.example.com",
        username: "sr-user",
        password: "sr-p@ss",
        insecure_skip_verify: true,
      },
      created_at: 1767261600000,
      updated_at: 1767348000000,
    });
    expect(getPrivateClusterByName("legacy")?.is_prod).toBeUndefined();
  });

  it("leaves the stored string untouched on reads", () => {
    listPrivateClusters();
    getPrivateClusterByName("prod-eu");
    exportBundle();
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe(V1);
  });

  it("writes an unchanged cluster back byte-for-byte", () => {
    const prod = getPrivateClusterByName("prod-eu");
    if (!prod) throw new Error("fixture not parsed");
    vi.useFakeTimers();
    vi.setSystemTime(new Date(prod.updated_at));
    const { created_at: _c, updated_at: _u, ...input } = prod;
    upsertPrivateCluster(input);
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe(V1);
  });

  it("round-trips through export and import unchanged", () => {
    const bundle = JSON.stringify(exportBundle());
    window.localStorage.clear();
    expect(importBundle(bundle)).toEqual({ added: 3, updated: 0, skipped: 0 });
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe(V1);
  });

  it("sends the stored credentials to the backend in the cluster header", () => {
    const prod = getPrivateClusterByName("prod-eu");
    if (!prod) throw new Error("fixture not parsed");
    const decoded = JSON.parse(
      decodeURIComponent(escape(atob(encodePrivateClusterHeader(prod)))),
    ) as unknown;
    expect(decoded).toEqual({
      name: "prod-eu",
      is_prod: true,
      brokers: ["kafka-1.example.com:9093", "kafka-2.example.com:9093"],
      auth: { type: "scram-sha-512", username: "svc-kafkito", password: "s3cr3t/p%ss #1" },
      tls: { enabled: true, insecure_skip_verify: false },
      schema_registry: {
        url: "https://sr.example.com",
        username: "sr-user",
        password: "sr-p@ss",
        insecure_skip_verify: true,
      },
    });
  });
});
