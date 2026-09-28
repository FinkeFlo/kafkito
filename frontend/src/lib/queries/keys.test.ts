import { describe, expect, it } from "vitest";
import { aclQueries } from "./acls";
import { authKeys, authQueries } from "./auth";
import { brokerQueries } from "./brokers";
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

describe("query keys", () => {
  it("are stable", () => {
    const params = { from: "end" as const, limit: 50 };
    const range = { from_ts_ms: 1, to_ts_ms: 2, slot_ms: 1 };
    const body = { topic: T, strategy: "earliest" as const };
    expect({
      authCurrentUser: authQueries.currentUser().queryKey,
      authMe: authQueries.me().queryKey,
      authAll: authKeys.all,
      info: infoQueries.info().queryKey,
      clusters: clusterQueries.list().queryKey,
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
      groups: groupQueries.list(C).queryKey,
      group: groupQueries.detail(C, "g").queryKey,
      resetPreview: groupQueries.resetPreview(C, "g", body, true).queryKey,
      schemas: schemaQueries.subjects(C).queryKey,
      schemaSubject: schemaKeys.subject(C, "s"),
      schema: schemaQueries.version(C, "s", 3).queryKey,
      schemaLatest: schemaQueries.version(C, "s", "latest").queryKey,
      acls: aclQueries.list(C).queryKey,
      scramUsers: scramUserQueries.list(C).queryKey,
    }).toEqual({
      authCurrentUser: ["auth", "currentUser"],
      authMe: ["auth", "me"],
      authAll: ["auth"],
      info: ["info"],
      clusters: ["clusters"],
      brokers: ["brokers", C],
      topics: ["topics", C],
      topic: ["topic", C, T],
      topicConsumers: ["topic-consumers", C, T],
      messagesPrefix: ["messages", C, T],
      messages: ["messages", C, T, params],
      latestProbe: ["produce-latest-probe", C, T],
      count: ["message-count", C, T, { partition: 1 }],
      timeline: ["message-timeline", C, T, -1, range],
      sample: ["sample", C, T, "xml"],
      raw: ["message-raw", C, T, 0, 5],
      groups: ["groups", C],
      group: ["group", C, "g"],
      resetPreview: ["reset-offsets-preview", C, "g", body],
      schemas: ["schemas", C],
      schemaSubject: ["schema", C, "s"],
      schema: ["schema", C, "s", 3],
      schemaLatest: ["schema", C, "s", "latest"],
      acls: ["acls", C],
      scramUsers: ["scram-users", C],
    });
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
