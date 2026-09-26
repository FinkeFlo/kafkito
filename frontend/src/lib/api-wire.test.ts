import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearCsrfToken } from "../auth/csrf";
import * as api from "./api";
import type { PrivateCluster } from "./private-clusters";

// Wire-level contract of every endpoint function in ./api: method, URL
// (including percent-encoding of names with `/`, `%`, `#`, `?` and spaces),
// headers, body, parsed result and the error message the UI shows. The
// snapshot is the reference; any change to it is a behaviour change of the
// HTTP layer and must be deliberate.

const SHARED = "prod/eu #1 %";
const PRIVATE = "mine/dev #2 %";
const TOPIC = "orders/v1 %2F #x ?y";
const GROUP = "grp/ä %";
const SUBJECT = "sub/j #%";
const USER = "alice/b%#";

const PRIVATE_FIXTURE: PrivateCluster = {
  id: "pc_wire",
  name: PRIVATE,
  is_prod: true,
  brokers: ["broker-1:9093", "broker-2:9093"],
  auth: { type: "scram-sha-512", username: "svc", password: "s3cr3t" },
  tls: { enabled: true, insecure_skip_verify: false },
  schema_registry: { url: "https://sr.example", username: "sr", password: "srpw" },
  created_at: 1700000000000,
  updated_at: 1700000000001,
};

interface Recorded {
  method: string;
  url: string;
  headers: Record<string, string>;
  credentials?: string;
  body?: string;
  gzip?: boolean;
}

type Reply = () => Response;

let calls: Recorded[] = [];
let signals: AbortSignal[] = [];
let reply: Reply = () => okJSON();

const EVERYTHING = {
  clusters: [],
  topics: [],
  brokers: [],
  topic: { name: "t" },
  consumers: [],
  messages: [],
  groups: [],
  subjects: [],
  acls: [],
  users: [],
  deleted: 1,
};

function okJSON(body: unknown = EVERYTHING): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

async function bodyText(body: unknown, gz: boolean): Promise<string | undefined> {
  if (body === undefined || body === null) return undefined;
  if (typeof body === "string") return body;
  let blob = body instanceof Blob ? body : new Blob([body as ArrayBuffer]);
  if (gz) {
    blob = await new Response(blob.stream().pipeThrough(new DecompressionStream("gzip"))).blob();
  }
  return await blob.text();
}

async function record(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const isReq = typeof input === "object" && "url" in input && !(input instanceof URL);
  const req = isReq ? (input as Request) : null;
  const url = req ? req.url : new URL(String(input), window.location.href).href;
  const headers = new Headers(init.headers ?? req?.headers ?? {});
  const method = (init.method ?? req?.method ?? "GET").toUpperCase();
  if (url.endsWith("/") && method === "HEAD") {
    return new Response(null, { status: 200, headers: { "x-csrf-token": "tok" } });
  }
  const gzip = headers.get("content-encoding") === "gzip";
  const rawBody = init.body ?? (req?.body ? await req.blob() : undefined);
  const signal = init.signal ?? req?.signal;
  if (signal) signals.push(signal);
  const entry: Recorded = {
    method,
    url: url.replace(window.location.origin, ""),
    headers: Object.fromEntries([...headers.entries()].map(([k, v]) => [k.toLowerCase(), v])),
    credentials: init.credentials ?? req?.credentials,
  };
  const text = await bodyText(rawBody, gzip);
  if (text !== undefined) entry.body = text.length > 200 ? `<${text.length} chars>` : text;
  if (gzip) entry.gzip = true;
  calls.push(entry);
  return reply();
}

const PRODUCE: api.ProduceRequest = { key: "k", value: '{"a":1}', headers: { h: "v" } };

// One entry per endpoint function. `run` must use the given cluster for
// cluster-scoped endpoints.
const ENDPOINTS: Record<string, (c: string) => Promise<unknown>> = {
  fetchInfo: () => api.fetchInfo(),
  fetchClusters: () => api.fetchClusters(),
  fetchTopics: (c) => api.fetchTopics(c),
  fetchBrokers: (c) => api.fetchBrokers(c),
  fetchTopicDetail: (c) => api.fetchTopicDetail(c, TOPIC),
  fetchTopicConsumers: (c) => api.fetchTopicConsumers(c, TOPIC),
  "fetchMessages(all params)": (c) =>
    api.fetchMessages(c, TOPIC, {
      partition: 2,
      limit: 50,
      from: "offset",
      offset: 10,
      from_ts_ms: 1,
      to_ts_ms: 2,
      cursor: "a+b/c= d",
    }),
  "fetchMessages(partitionOffsets)": (c) =>
    api.fetchMessages(c, TOPIC, {
      partition: -1,
      from: "offset",
      partitionOffsets: { 0: 5, 1: 7 },
    }),
  "fetchMessages(none)": (c) => api.fetchMessages(c, TOPIC),
  "fetchMessageCount(params)": (c) =>
    api.fetchMessageCount(c, TOPIC, { partition: 1, from_ts_ms: 1, to_ts_ms: 2 }),
  "fetchMessageCount(none)": (c) => api.fetchMessageCount(c, TOPIC),
  "fetchMessageTimeline(all)": (c) =>
    api.fetchMessageTimeline(c, TOPIC, { partition: -1, from_ts_ms: 1, to_ts_ms: 9, slot_ms: 3 }),
  "fetchMessageTimeline(p0)": (c) =>
    api.fetchMessageTimeline(c, TOPIC, { partition: 0, from_ts_ms: 1, to_ts_ms: 9, slot_ms: 3 }),
  "fetchSample(default)": (c) => api.fetchSample(c, TOPIC),
  "fetchSample(3,1)": (c) => api.fetchSample(c, TOPIC, 3, 1),
  "produceMessage(no confirm)": (c) => api.produceMessage(c, TOPIC, PRODUCE),
  "produceMessage(confirm)": (c) => api.produceMessage(c, TOPIC, PRODUCE, true),
  "produceMessage(gzip)": (c) =>
    api.produceMessage(c, TOPIC, { ...PRODUCE, value: "x".repeat(300 * 1024) }, true),
  fetchGroups: (c) => api.fetchGroups(c),
  fetchGroupDetail: (c) => api.fetchGroupDetail(c, GROUP),
  "resetGroupOffsets(no confirm)": (c) =>
    api.resetGroupOffsets(c, GROUP, { topic: TOPIC, strategy: "earliest", dry_run: true }),
  "resetGroupOffsets(confirm)": (c) =>
    api.resetGroupOffsets(c, GROUP, { topic: TOPIC, strategy: "latest" }, true),
  createGroup: (c) =>
    api.createGroup(c, { group_id: GROUP, topic: TOPIC, strategy: "earliest", dry_run: false }),
  deleteGroup: (c) => api.deleteGroup(c, GROUP),
  createTopic: (c) => api.createTopic(c, { name: TOPIC, partitions: 3, replication_factor: 1 }),
  listSubjects: (c) => api.listSubjects(c),
  "getSchemaVersion(latest)": (c) => api.getSchemaVersion(c, SUBJECT, "latest"),
  "getSchemaVersion(3)": (c) => api.getSchemaVersion(c, SUBJECT, 3),
  "deleteSubject(soft)": (c) => api.deleteSubject(c, SUBJECT),
  "deleteSubject(permanent)": (c) => api.deleteSubject(c, SUBJECT, true),
  alterTopicConfigs: (c) =>
    api.alterTopicConfigs(c, TOPIC, {
      set: { "retention.ms": "1000" },
      delete: ["cleanup.policy"],
    }),
  listACLs: (c) => api.listACLs(c),
  createACL: (c) => api.createACL(c, ACL),
  deleteACL: (c) => api.deleteACL(c, ACL),
  searchMessages: (c) =>
    api.searchMessages(c, TOPIC, { mode: "contains", path: "", op: "eq", value: "x", limit: 5 }),
  listSCRAMUsers: (c) => api.listSCRAMUsers(c),
  upsertSCRAMUser: (c) =>
    api.upsertSCRAMUser(c, { user: USER, mechanism: "SCRAM-SHA-256", password: "pw" }),
  "deleteSCRAMUser(all)": (c) => api.deleteSCRAMUser(c, USER),
  "deleteSCRAMUser(mechanism)": (c) => api.deleteSCRAMUser(c, USER, "SCRAM-SHA-512"),
  testCluster: () => api.testCluster(PRIVATE_FIXTURE),
  fetchMessageRawBase64: (c) => api.fetchMessageRawBase64(c, TOPIC, 0, 5),
  downloadMessageRaw: (c) => api.downloadMessageRaw(c, TOPIC, 0, 5),
};

const ACL: api.ACLSpec = {
  resource_type: "topic",
  resource_name: TOPIC,
  pattern_type: "literal",
  principal: "User:alice",
  host: "*",
  operation: "read",
  permission_type: "allow",
};

function seedPrivateCluster() {
  window.localStorage.setItem("kafkito.private-clusters.v1", JSON.stringify([PRIVATE_FIXTURE]));
}

beforeEach(() => {
  calls = [];
  signals = [];
  reply = () => okJSON();
  clearCsrfToken();
  window.localStorage.clear();
  vi.stubGlobal("fetch", vi.fn(record));
  URL.createObjectURL = vi.fn(() => "blob:x");
  URL.revokeObjectURL = vi.fn();
  // downloadMessageRaw clicks a blob: link; don't let happy-dom navigate.
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// Stubs fetch with `fail` for API calls while still answering the CSRF
// token probe (HEAD /) that precedes every write.
function stubFetch(fail: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/" && init?.method === "HEAD") return record(input, init);
      return fail(input, init);
    }),
  );
}

async function outcome(fn: () => Promise<unknown>): Promise<unknown> {
  try {
    const result = await fn();
    return { result };
  } catch (e) {
    return { error: `${(e as Error).constructor.name}: ${(e as Error).message}` };
  }
}

describe("api wire contract", () => {
  for (const [label, cluster] of [
    ["shared cluster", SHARED],
    ["private cluster", PRIVATE],
  ] as const) {
    it(`requests for a ${label}`, async () => {
      seedPrivateCluster();
      const out: Record<string, unknown> = {};
      for (const [name, run] of Object.entries(ENDPOINTS)) {
        calls = [];
        const o = await outcome(() => run(cluster));
        out[name] = { ...(o as object), requests: calls };
      }
      expect(out).toMatchSnapshot();
    });
  }

  it("error messages for JSON error bodies", async () => {
    reply = () =>
      new Response('{"error":"boom","code":"x"}\n', {
        status: 500,
        statusText: "Internal Server Error",
        headers: { "Content-Type": "application/json" },
      });
    const out: Record<string, unknown> = {};
    for (const [name, run] of Object.entries(ENDPOINTS))
      out[name] = await outcome(() => run(SHARED));
    expect(out).toMatchSnapshot();
  });

  it("error messages for plain-text error bodies", async () => {
    reply = () => new Response("bad gateway page", { status: 502, statusText: "Bad Gateway" });
    const out: Record<string, unknown> = {};
    for (const [name, run] of Object.entries(ENDPOINTS))
      out[name] = await outcome(() => run(SHARED));
    expect(out).toMatchSnapshot();
  });

  it("error messages for empty error bodies", async () => {
    reply = () => new Response("", { status: 503, statusText: "Service Unavailable" });
    const out: Record<string, unknown> = {};
    for (const [name, run] of Object.entries(ENDPOINTS))
      out[name] = await outcome(() => run(SHARED));
    expect(out).toMatchSnapshot();
  });

  it("never puts the private-cluster header value into an error message", async () => {
    seedPrivateCluster();
    reply = () => new Response('{"error":"nope"}', { status: 400 });
    for (const run of Object.values(ENDPOINTS)) {
      const o = (await outcome(() => run(PRIVATE))) as { error?: string };
      expect(o.error ?? "").not.toContain("s3cr3t");
      expect(o.error ?? "").not.toMatch(/eyJ/);
    }
  });

  it("forwards the caller's abort signal to fetch", async () => {
    const ctrl = new AbortController();
    await api.fetchMessageRawBase64(SHARED, TOPIC, 0, 5, ctrl.signal);
    expect(signals).toHaveLength(1);
    expect(signals[0].aborted).toBe(false);
    ctrl.abort();
    expect(signals[0].aborted).toBe(true);
  });

  it("streams bulk-copy progress events and aborts the stream", async () => {
    const enc = new TextEncoder();
    reply = () =>
      new Response(
        new ReadableStream({
          start(c) {
            c.enqueue(enc.encode('data: {"copied":1,"done":false}\n\ndata: {"cop'));
            c.enqueue(enc.encode('ied":2,"done":true}\n\n: comment\ndata: not-json\n\n'));
            c.close();
          },
        }),
        { status: 200, headers: { "Content-Type": "text/event-stream" } },
      );
    seedPrivateCluster();
    const events: unknown[] = [];
    const abort = api.copyMessages(
      PRIVATE,
      TOPIC,
      { dest_cluster: SHARED, dest_topic: "d", limit: 2 },
      true,
      (ev) => events.push(ev),
    );
    await vi.waitFor(() => expect(events).toHaveLength(2));
    expect({ events, requests: calls }).toMatchSnapshot();
    abort();
    expect(signals.at(-1)?.aborted).toBe(true);
  });

  it("reports bulk-copy HTTP errors through onProgress", async () => {
    reply = () => new Response('{"error":"denied"}', { status: 403 });
    const events: unknown[] = [];
    api.copyMessages(SHARED, TOPIC, { dest_cluster: SHARED, dest_topic: "d" }, false, (ev) =>
      events.push(ev),
    );
    await vi.waitFor(() => expect(events).toHaveLength(1));
    expect(events).toEqual([{ copied: 0, done: true, error: "HTTP 403: denied" }]);
  });

  it("reports bulk-copy network errors through onProgress but stays silent on abort", async () => {
    stubFetch(async () => {
      throw new TypeError("Failed to fetch");
    });
    const events: unknown[] = [];
    api.copyMessages(SHARED, TOPIC, { dest_cluster: SHARED, dest_topic: "d" }, false, (ev) =>
      events.push(ev),
    );
    await vi.waitFor(() => expect(events).toHaveLength(1));
    expect(events).toEqual([{ copied: 0, done: true, error: "Failed to fetch" }]);

    stubFetch(async () => {
      throw new DOMException("aborted", "AbortError");
    });
    const silent: unknown[] = [];
    api.copyMessages(SHARED, TOPIC, { dest_cluster: SHARED, dest_topic: "d" }, false, (ev) =>
      silent.push(ev),
    );
    await new Promise((r) => setTimeout(r, 20));
    expect(silent).toEqual([]);
  });

  it("maps an aborted cluster probe to the timeout message", async () => {
    vi.useFakeTimers();
    try {
      stubFetch(async (input, init) => {
        const signal = init?.signal ?? (input as Request).signal;
        return await new Promise<Response>((_, reject) => {
          signal?.addEventListener("abort", () =>
            reject(new DOMException("aborted", "AbortError")),
          );
        });
      });
      const p = api.testCluster(PRIVATE_FIXTURE).catch((e: Error) => e.message);
      await vi.advanceTimersByTimeAsync(20_000);
      expect(await p).toBe("timed out probing brokers — try again, broker DNS may be cold");
    } finally {
      vi.useRealTimers();
    }
  });
});
