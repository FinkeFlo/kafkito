import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PrivateCluster } from "./private-clusters";

// Session passwords live in module state, so every test loads a fresh copy
// of the module: one test's tab must not leak into the next.
async function load() {
  vi.resetModules();
  return import("./private-clusters");
}

const STORAGE_KEY = "kafkito.private-clusters.v1";

function sample(
  remember?: boolean,
): Omit<PrivateCluster, "id" | "created_at" | "updated_at"> & { id?: string } {
  return {
    name: "dev",
    brokers: ["10.0.0.5:9092"],
    auth: { type: "scram-sha-512", username: "u", password: "kafka-pw" },
    tls: { enabled: true },
    schema_registry: { url: "https://sr.example.com", username: "sr", password: "sr-pw" },
    ...(remember === undefined ? {} : { remember_credentials: remember }),
  };
}

function stored(): string {
  return window.localStorage.getItem(STORAGE_KEY) ?? "";
}

describe("private cluster passwords that are not remembered", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("keeps them out of localStorage but serves them in this tab", async () => {
    const pc = await load();
    const saved = pc.upsertPrivateCluster(sample(false));

    expect(stored()).not.toContain("kafka-pw");
    expect(stored()).not.toContain("sr-pw");
    expect(stored()).toContain('"remember_credentials":false');

    const read = pc.getPrivateClusterByName("dev");
    expect(read?.auth.password).toBe("kafka-pw");
    expect(read?.schema_registry?.password).toBe("sr-pw");
    expect(pc.getPrivateClusterById(saved.id)?.auth.password).toBe("kafka-pw");
    expect(pc.listPrivateClusters()[0].auth.password).toBe("kafka-pw");
    expect(pc.missingPasswords(read!)).toEqual({ password: false, srPassword: false });
  });

  it("are gone in a new tab, which has to ask for them", async () => {
    (await load()).upsertPrivateCluster(sample(false));

    const pc = await load();
    const read = pc.getPrivateClusterByName("dev")!;
    expect(read.auth.password).toBeUndefined();
    expect(read.schema_registry?.password).toBeUndefined();
    expect(pc.missingPasswords(read)).toEqual({ password: true, srPassword: true });
    expect(() => pc.encodePrivateClusterHeader(read)).toThrow(pc.PasswordRequiredError);

    pc.setSessionPasswords(read.id, { password: "kafka-pw", srPassword: "sr-pw" });
    const unlocked = pc.getPrivateClusterByName("dev")!;
    expect(unlocked.auth.password).toBe("kafka-pw");
    expect(pc.missingPasswords(unlocked)).toEqual({ password: false, srPassword: false });
    expect(stored()).not.toContain("kafka-pw");
  });

  it("notifies subscribers when passwords are entered", async () => {
    const pc = await load();
    const saved = pc.upsertPrivateCluster(sample(false));
    const cb = vi.fn();
    const unsub = pc.subscribePrivateClusters(cb);
    pc.setSessionPasswords(saved.id, { password: "x" });
    expect(cb).toHaveBeenCalled();
    unsub();
  });

  it("are written to localStorage once remember_credentials is turned on", async () => {
    const pc = await load();
    const saved = pc.upsertPrivateCluster(sample(false));
    pc.upsertPrivateCluster({ ...sample(true), id: saved.id });
    expect(stored()).toContain("kafka-pw");
    expect(stored()).toContain("sr-pw");
  });

  it("leave localStorage when remember_credentials is turned off", async () => {
    const pc = await load();
    const saved = pc.upsertPrivateCluster(sample(true));
    expect(stored()).toContain("kafka-pw");
    pc.upsertPrivateCluster({ ...sample(false), id: saved.id });
    expect(stored()).not.toContain("kafka-pw");
    expect(pc.getPrivateClusterById(saved.id)?.auth.password).toBe("kafka-pw");
  });

  it("stay out of localStorage when an update or import leaves the setting out", async () => {
    const pc = await load();
    const saved = pc.upsertPrivateCluster(sample(false));
    pc.upsertPrivateCluster({ ...sample(), id: saved.id, remember_credentials: undefined });
    expect(stored()).not.toContain("kafka-pw");
    expect(stored()).toContain('"remember_credentials":false');

    // An export from before the setting existed, with passwords.
    const legacy = JSON.stringify({
      schema: "kafkito.private-clusters/v1",
      exported_at: "2026-01-01T00:00:00.000Z",
      clusters: [{ ...sample(), id: saved.id, created_at: 0, updated_at: 0 }],
    });
    expect(pc.importBundle(legacy).updated).toBe(1);
    expect(stored()).not.toContain("kafka-pw");
    expect(stored()).not.toContain("sr-pw");
    expect(pc.getPrivateClusterById(saved.id)?.auth.password).toBe("kafka-pw");
  });

  it("are forgotten with the cluster", async () => {
    const pc = await load();
    const saved = pc.upsertPrivateCluster(sample(false));
    pc.deletePrivateCluster(saved.id);
    // Same id saved again without passwords: nothing from before comes back.
    pc.upsertPrivateCluster({
      ...sample(false),
      id: saved.id,
      auth: { type: "scram-sha-512", username: "u" },
      schema_registry: { url: "https://sr.example.com", username: "sr" },
    });
    expect(pc.getPrivateClusterById(saved.id)?.auth.password).toBeUndefined();
  });

  it("stay out of exports, and import keeps passwords of such entries out of storage", async () => {
    const pc = await load();
    pc.upsertPrivateCluster(sample(false));
    const exported = JSON.stringify(pc.exportBundle());
    expect(exported).not.toContain("kafka-pw");
    expect(exported).toContain('"remember_credentials":false');

    // A file that carries passwords for such an entry.
    const withPw = JSON.stringify({
      schema: "kafkito.private-clusters/v1",
      exported_at: "2026-01-01T00:00:00.000Z",
      clusters: [{ ...sample(false), id: "pc_x", created_at: 0, updated_at: 0 }],
    });
    window.localStorage.clear();
    const fresh = await load();
    fresh.importBundle(withPw);
    expect(stored()).not.toContain("kafka-pw");
    expect(fresh.getPrivateClusterByName("dev")?.auth.password).toBe("kafka-pw");
  });

  it("are not sent to settings they were not entered for", async () => {
    const pc = await load();
    const saved = pc.upsertPrivateCluster(sample(false));
    const raw = JSON.parse(stored()) as PrivateCluster[];

    // Another tab points the SASL settings elsewhere: the password is gone.
    raw[0] = { ...raw[0], brokers: ["203.0.113.9:9092"] };
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(raw));
    let c = pc.getPrivateClusterById(saved.id)!;
    expect(c.auth.password).toBeUndefined();
    expect(c.schema_registry?.password).toBe("sr-pw");

    // Turning TLS off counts as well, and so does a new registry URL.
    raw[0] = {
      ...raw[0],
      brokers: ["10.0.0.5:9092"],
      tls: { enabled: false },
      schema_registry: {
        url: "https://evil.example.com",
        username: "sr",
        credential_required: true,
      },
    };
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(raw));
    c = pc.getPrivateClusterById(saved.id)!;
    expect(c.auth.password).toBeUndefined();
    expect(c.schema_registry?.password).toBeUndefined();
    expect(pc.missingPasswords(c)).toEqual({ password: true, srPassword: true });
  });
});

describe("remembered passwords", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("stay in localStorage, also for entries saved before the setting existed", async () => {
    const pc = await load();
    pc.upsertPrivateCluster(sample());
    expect(stored()).toContain("kafka-pw");
    expect(stored()).not.toContain("remember_credentials");

    const next = await load();
    const read = next.getPrivateClusterByName("dev")!;
    expect(read.auth.password).toBe("kafka-pw");
    expect(next.missingPasswords(read)).toEqual({ password: false, srPassword: false });
  });
});

describe("missingPasswords", () => {
  it("asks only for credentials the cluster uses", async () => {
    const pc = await load();
    const base = {
      id: "x",
      created_at: 0,
      updated_at: 0,
      name: "n",
      brokers: ["h:1"],
      remember_credentials: false,
    };
    expect(
      pc.missingPasswords({ ...base, auth: { type: "none" }, tls: { enabled: true } }),
    ).toEqual({ password: false, srPassword: false });
    expect(
      pc.missingPasswords({
        ...base,
        auth: { type: "none" },
        tls: { enabled: true },
        schema_registry: { url: "https://sr" },
      }),
    ).toEqual({ password: false, srPassword: false });
    expect(
      pc.missingPasswords({
        ...base,
        auth: { type: "plain", username: "u" },
        tls: { enabled: true },
        schema_registry: { url: "https://sr", username: "s", credential_required: true },
      }),
    ).toEqual({ password: true, srPassword: true });
    // A registry user saved without a password is complete.
    expect(
      pc.missingPasswords({
        ...base,
        auth: { type: "none" },
        tls: { enabled: true },
        schema_registry: { url: "https://sr", username: "s", credential_required: false },
      }),
    ).toEqual({ password: false, srPassword: false });
  });

  it("never asks for a remembered cluster's passwords, even if one is missing", async () => {
    const pc = await load();
    pc.upsertPrivateCluster({
      ...sample(true),
      schema_registry: { url: "https://sr.example.com", username: "sr" },
    });
    const c = pc.getPrivateClusterByName("dev")!;
    expect(pc.missingPasswords(c)).toEqual({ password: false, srPassword: false });
    expect(() => pc.encodePrivateClusterHeader(c)).not.toThrow();
  });
});

describe("a cluster saved without a Schema Registry password", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("is complete in a new tab once the SASL password is entered", async () => {
    (await load()).upsertPrivateCluster({
      ...sample(false),
      schema_registry: { url: "https://sr.example.com", username: "sr" },
    });
    const pc = await load();
    const c = pc.getPrivateClusterByName("dev")!;
    expect(pc.missingPasswords(c)).toEqual({ password: true, srPassword: false });
    pc.setSessionPasswords(c.id, { password: "kafka-pw" });
    expect(pc.needsPasswords(pc.getPrivateClusterByName("dev")!)).toBe(false);
  });

  it("drops a held registry password the form cleared", async () => {
    const pc = await load();
    const saved = pc.upsertPrivateCluster(sample(false));
    pc.upsertPrivateCluster({
      ...sample(false),
      id: saved.id,
      schema_registry: { url: "https://sr.example.com", username: "sr" },
    });
    const c = pc.getPrivateClusterById(saved.id)!;
    expect(c.schema_registry?.password).toBeUndefined();
    expect(c.auth.password).toBe("kafka-pw");
    expect(pc.missingPasswords(c)).toEqual({ password: false, srPassword: false });
  });
});
