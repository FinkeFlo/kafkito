import { afterEach, describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { PrivateCluster } from "../private-clusters";
import { aclQueries } from "./acls";
import { authKeys, authQueries } from "./auth";
import { brokerQueries } from "./brokers";
import { clusterKey } from "./cluster-key";
import { clusterQueries } from "./clusters";
import { groupQueries } from "./groups";
import { infoQueries } from "./info";
import { messageKeys, messageQueries } from "./messages";
import { schemaKeys, schemaQueries } from "./schemas";
import { topicQueries } from "./topics";
import { scramUserQueries } from "./users";

// Query keys are cache identities: changing one drops or splits cache
// entries and silently breaks invalidation. Pin every key exactly.

const C = "prod/eu #1";
const T = "orders %";
const PRIVATE_ID = "pc_same_name";

const params = { from: "end" as const, limit: 50 };
const range = { from_ts_ms: 1, to_ts_ms: 2, slot_ms: 1 };
const body = { topic: T, strategy: "earliest" as const };

function storePrivateCluster(name: string, id = PRIVATE_ID) {
  const pc: PrivateCluster = {
    id,
    name,
    brokers: ["10.0.0.1:9092"],
    auth: { type: "none" },
    tls: { enabled: false },
    created_at: 0,
    updated_at: 0,
  };
  localStorage.setItem("kafkito.private-clusters.v1", JSON.stringify([pc]));
}

/** Every key that belongs to one cluster, built for the cluster named `C`. */
function clusterScopedKeys() {
  return {
    brokers: brokerQueries.list(C).queryKey,
    topics: topicQueries.list(C).queryKey,
    topic: topicQueries.detail(C, T).queryKey,
    topicConsumers: topicQueries.consumers(C, T).queryKey,
    messagesPrefix: messageKeys.topic(C, T),
    messages: messageQueries.page(C, T, params).queryKey,
    latestProbe: messageQueries.latestProbe(C, T).queryKey,
    count: messageQueries.count(C, T, { partition: 1 }).queryKey,
    timeline: messageQueries.timeline(C, T, -1, range).queryKey,
    sample: messageQueries.sample(C, T, "xml").queryKey,
    raw: messageQueries.raw(C, T, 0, 5).queryKey,
    rawWire: messageQueries.raw(C, T, 0, 5, false).queryKey,
    groups: groupQueries.list(C).queryKey,
    group: groupQueries.detail(C, "g").queryKey,
    resetPreview: groupQueries.resetPreview(C, "g", body, true).queryKey,
    schemas: schemaQueries.subjects(C).queryKey,
    schemaSubject: schemaKeys.subject(C, "s"),
    schema: schemaQueries.version(C, "s", 3).queryKey,
    schemaLatest: schemaQueries.version(C, "s", "latest").queryKey,
    acls: aclQueries.list(C).queryKey,
    scramUsers: scramUserQueries.list(C).queryKey,
  };
}

/** The keys `clusterScopedKeys` must produce when the cluster's identity is `id`. */
function expectedClusterScopedKeys(id: unknown) {
  return {
    brokers: ["brokers", id],
    topics: ["topics", id],
    topic: ["topic", id, T],
    topicConsumers: ["topic-consumers", id, T],
    messagesPrefix: ["messages", id, T],
    messages: ["messages", id, T, params],
    latestProbe: ["produce-latest-probe", id, T],
    count: ["message-count", id, T, { partition: 1 }],
    timeline: ["message-timeline", id, T, -1, range],
    sample: ["sample", id, T, "xml"],
    raw: ["message-raw", id, T, 0, 5, true],
    rawWire: ["message-raw", id, T, 0, 5, false],
    groups: ["groups", id],
    group: ["group", id, "g"],
    resetPreview: ["reset-offsets-preview", id, "g", body],
    schemas: ["schemas", id],
    schemaSubject: ["schema", id, "s"],
    schema: ["schema", id, "s", 3],
    schemaLatest: ["schema", id, "s", "latest"],
    acls: ["acls", id],
    scramUsers: ["scram-users", id],
  };
}

afterEach(() => localStorage.clear());

describe("query keys", () => {
  it("are stable", () => {
    expect({
      authCurrentUser: authQueries.currentUser().queryKey,
      authMe: authQueries.me().queryKey,
      authAll: authKeys.all,
      info: infoQueries.info().queryKey,
      clusters: clusterQueries.list().queryKey,
      ...clusterScopedKeys(),
    }).toEqual({
      authCurrentUser: ["auth", "currentUser"],
      authMe: ["auth", "me"],
      authAll: ["auth"],
      info: ["info"],
      clusters: ["clusters"],
      ...expectedClusterScopedKeys(C),
    });
  });

  it("key a private cluster by its id, not by the name it shares with a shared cluster", () => {
    const shared = clusterScopedKeys();
    storePrivateCluster(C);
    const priv = clusterScopedKeys();

    expect(priv).toEqual(expectedClusterScopedKeys({ private: PRIVATE_ID }));
    for (const name of Object.keys(shared) as (keyof typeof shared)[]) {
      expect(priv[name], name).not.toEqual(shared[name]);
    }
  });

  it("give two private clusters that held the same name different identities", () => {
    storePrivateCluster(C, "pc_first");
    const first = clusterKey(C);
    storePrivateCluster(C, "pc_second");
    expect(clusterKey(C)).not.toEqual(first);
  });

  it("keep the message-page key under the prefix used for invalidation", () => {
    const page = messageQueries.page(C, T, {}).queryKey;
    expect(page.slice(0, 3)).toEqual([...messageKeys.topic(C, T)]);
  });

  it("keep schema versions under the subject prefix used for invalidation", () => {
    const latest = schemaQueries.version(C, "s", "latest").queryKey;
    expect(latest.slice(0, 3)).toEqual([...schemaKeys.subject(C, "s")]);
  });
});

describe("invalidation with a private and a shared cluster of the same name", () => {
  // Seeds both clusters' entries, then invalidates through the factories the
  // way the mutations do. Only the entries of the cluster the factory
  // resolves to may go stale.
  function seed(qc: QueryClient) {
    const sharedKeys = clusterScopedKeys();
    for (const key of Object.values(sharedKeys)) qc.setQueryData(key, "shared");
    storePrivateCluster(C);
    const privateKeys = clusterScopedKeys();
    for (const key of Object.values(privateKeys)) qc.setQueryData(key, "private");
    return { sharedKeys, privateKeys };
  }

  it("hits only the private cluster's entries while it holds the name", async () => {
    const qc = new QueryClient();
    const { sharedKeys, privateKeys } = seed(qc);

    await qc.invalidateQueries({ queryKey: topicQueries.list(C).queryKey });
    await qc.invalidateQueries({ queryKey: messageKeys.topic(C, T) });
    await qc.invalidateQueries({ queryKey: schemaQueries.subjects(C).queryKey });
    await qc.invalidateQueries({ queryKey: schemaKeys.subject(C, "s") });

    for (const name of ["topics", "messages", "schemas", "schema", "schemaLatest"] as const) {
      expect(qc.getQueryState(privateKeys[name])?.isInvalidated, name).toBe(true);
      expect(qc.getQueryState(sharedKeys[name])?.isInvalidated, name).toBe(false);
    }
    expect(qc.getQueryData(topicQueries.list(C).queryKey)).toBe("private");
  });

  it("hits only the shared cluster's entries once the private one is gone", async () => {
    const qc = new QueryClient();
    const { sharedKeys, privateKeys } = seed(qc);
    localStorage.clear();

    await qc.invalidateQueries({ queryKey: groupQueries.list(C).queryKey });
    await qc.invalidateQueries({ queryKey: aclQueries.list(C).queryKey });

    for (const name of ["groups", "acls"] as const) {
      expect(qc.getQueryState(sharedKeys[name])?.isInvalidated, name).toBe(true);
      expect(qc.getQueryState(privateKeys[name])?.isInvalidated, name).toBe(false);
    }
    expect(qc.getQueryData(topicQueries.list(C).queryKey)).toBe("shared");
  });
});
