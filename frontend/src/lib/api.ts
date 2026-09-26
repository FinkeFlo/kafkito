import { HttpError, client, withProdConfirm } from "./api-client";
import type { PrivateCluster } from "./private-clusters";
import { toBackendClusterConfig } from "./private-clusters";
import type { components, operations } from "./api.gen";

// API payload types are generated from api/openapi.yaml (see ADR-0005). Run
// `bun run api:generate` after editing the spec; do not hand-write DTOs here.
type Schemas = components["schemas"];

export type InfoResponse = Schemas["InfoResponse"];
export type Capabilities = Schemas["Capabilities"];
export type ClusterInfo = Schemas["ClusterInfo"];
export type TopicInfo = Schemas["TopicInfo"];
export type PartitionInfo = Schemas["PartitionInfo"];
export type TopicConfigEntry = Schemas["TopicConfigEntry"];
export type TopicDetail = Schemas["TopicDetail"];
export type Message = Schemas["Message"];
export type MessagesPage = Schemas["MessagesPage"];
export type MessageCountResponse = Schemas["MessageCountResponse"];
export type BrokerInfo = Schemas["BrokerInfo"];
export type TopicConsumer = Schemas["TopicConsumer"];
export type MessageTimelineSlot = Schemas["MessageTimelineSlot"];
export type MessageTimelineResponse = Schemas["MessageTimelineResponse"];
export type SampleResponse = Schemas["SampleResponse"];
export type ProduceRequest = Schemas["ProduceRequest"];
export type ProduceResult = Schemas["ProduceResult"];
export type CopyRequest = Schemas["CopyRequest"];
export type CopyProgressEvent = Schemas["CopyProgressEvent"];
export type GroupInfo = Schemas["GroupInfo"];
export type GroupOffset = Schemas["GroupOffset"];
export type GroupDetail = Schemas["GroupDetail"];
export type ResetOffsetsRequest = Schemas["ResetOffsetsRequest"];
export type ResetOffsetResult = Schemas["ResetOffsetResult"];
export type ResetOffsetsResult = Schemas["ResetOffsetsResponse"];
export type CreateGroupRequest = Schemas["CreateGroupRequest"];
export type CreateTopicRequest = Schemas["CreateTopicRequest"];
export type Subject = Schemas["Subject"];
export type SchemaVersion = Schemas["SchemaVersion"];
export type AlterTopicConfigsRequest = Schemas["AlterTopicConfigsRequest"];
export type ACLEntry = Schemas["ACLEntry"];
export type SearchRequest = Schemas["SearchRequest"];
export type SearchStats = Schemas["SearchStats"];
export type SearchResponse = Schemas["SearchResponse"];
export type SCRAMUser = Schemas["SCRAMUser"];
export type ACLSpec = ACLEntry;
export type SCRAMMechanism = Schemas["UpsertSCRAMUserRequest"]["mechanism"];
export type ResetStrategy = ResetOffsetsRequest["strategy"];
export type CreateGroupStrategy = CreateGroupRequest["strategy"];
export type SearchMode = NonNullable<SearchRequest["mode"]>;
export type SearchOp = NonNullable<SearchRequest["op"]>;
export type SearchDirection = NonNullable<SearchRequest["direction"]>;
type ConsumeQuery = NonNullable<operations["consumeMessages"]["parameters"]["query"]>;

/**
 * Query parameters of consumeMessages. `partitionOffsets` is the structured
 * form of the wire parameter `partition_offsets` ("p:o,p:o"): per-partition
 * seek offsets, used with `from: "offset"` when no single partition is
 * selected. Each listed partition is seeked to its offset, clamped
 * server-side to the partition's low watermark.
 */
export type ConsumeParams = Omit<ConsumeQuery, "partition_offsets"> & {
  partitionOffsets?: Record<number, number>;
};

// --- Errors -----------------------------------------------------------------
// Endpoints historically differ in how they turn a non-2xx response into the
// message the UI shows; each keeps its own mapping.

/** `HTTP <status>` plus `: <error>` when the body is an API Error. */
function statusError(e: HttpError): Error {
  const detail = e.json()?.error;
  return new Error(`HTTP ${e.status}${detail ? `: ${detail}` : ""}`);
}

/** The raw response body. */
function bodyError(e: HttpError): Error {
  return new Error(e.body);
}

/** The raw response body, or the status text when the body is empty. */
function bodyOrStatusTextError(e: HttpError): Error {
  return new Error(e.body || e.statusText);
}

/** Awaits an openapi-fetch call and maps an HttpError with `toError`. */
async function send<R>(request: Promise<R>, toError: (e: HttpError) => Error): Promise<R> {
  try {
    return await request;
  } catch (e) {
    throw e instanceof HttpError ? toError(e) : e;
  }
}

/** Like send, but resolves to the parsed response body. */
async function call<T>(
  request: Promise<{ data?: T }>,
  toError: (e: HttpError) => Error = statusError,
): Promise<T> {
  return (await send(request, toError)).data as T;
}

const ACCEPT_JSON = { Accept: "application/json" };

// --- Endpoints --------------------------------------------------------------

export function fetchInfo(): Promise<InfoResponse> {
  return call(client.GET("/api/v1/info", { headers: ACCEPT_JSON }));
}

export async function fetchClusters(): Promise<ClusterInfo[]> {
  const r = await call(client.GET("/api/v1/clusters", { headers: ACCEPT_JSON }));
  return r.clusters;
}

export async function fetchTopics(cluster: string): Promise<TopicInfo[]> {
  const r = await call(
    client.GET("/api/v1/clusters/{cluster}/topics", {
      params: { path: { cluster } },
      headers: ACCEPT_JSON,
    }),
  );
  return r.topics;
}

export async function fetchBrokers(cluster: string): Promise<BrokerInfo[]> {
  const r = await call(
    client.GET("/api/v1/clusters/{cluster}/brokers", {
      params: { path: { cluster } },
      headers: ACCEPT_JSON,
    }),
  );
  return r.brokers;
}

export async function fetchTopicDetail(cluster: string, topic: string): Promise<TopicDetail> {
  const r = await call(
    client.GET("/api/v1/clusters/{cluster}/topics/{topic}", {
      params: { path: { cluster, topic } },
      headers: ACCEPT_JSON,
    }),
  );
  return r.topic;
}

export async function fetchTopicConsumers(
  cluster: string,
  topic: string,
): Promise<TopicConsumer[]> {
  const r = await call(
    client.GET("/api/v1/clusters/{cluster}/topics/{topic}/consumers", {
      params: { path: { cluster, topic } },
      headers: ACCEPT_JSON,
    }),
  );
  return r.consumers ?? [];
}

/** `partition` is sent only for a concrete partition; -1 means all. */
function partitionParam(partition: number | undefined): number | undefined {
  return partition !== undefined && partition >= 0 ? partition : undefined;
}

export function fetchMessages(
  cluster: string,
  topic: string,
  params: ConsumeParams = {},
): Promise<MessagesPage> {
  const pairs = params.partitionOffsets
    ? Object.entries(params.partitionOffsets)
        .map(([p, o]) => `${p}:${o}`)
        .join(",")
    : "";
  return call(
    client.GET("/api/v1/clusters/{cluster}/topics/{topic}/messages", {
      params: {
        path: { cluster, topic },
        query: {
          partition: partitionParam(params.partition),
          limit: params.limit,
          from: params.from || undefined,
          offset: params.offset,
          partition_offsets: pairs || undefined,
          from_ts_ms: params.from_ts_ms,
          to_ts_ms: params.to_ts_ms,
          cursor: params.cursor || undefined,
        },
      },
      headers: ACCEPT_JSON,
    }),
  );
}

export function fetchMessageCount(
  cluster: string,
  topic: string,
  params: Pick<ConsumeParams, "partition" | "from_ts_ms" | "to_ts_ms"> = {},
): Promise<MessageCountResponse> {
  return call(
    client.GET("/api/v1/clusters/{cluster}/topics/{topic}/messages/count", {
      params: {
        path: { cluster, topic },
        query: {
          partition: partitionParam(params.partition),
          from_ts_ms: params.from_ts_ms,
          to_ts_ms: params.to_ts_ms,
        },
      },
      headers: ACCEPT_JSON,
    }),
  );
}

export function fetchMessageTimeline(
  cluster: string,
  topic: string,
  params: { partition?: number; from_ts_ms: number; to_ts_ms: number; slot_ms: number },
): Promise<MessageTimelineResponse> {
  return call(
    client.GET("/api/v1/clusters/{cluster}/topics/{topic}/messages/timeline", {
      params: {
        path: { cluster, topic },
        query: {
          partition: partitionParam(params.partition),
          from_ts_ms: params.from_ts_ms,
          to_ts_ms: params.to_ts_ms,
          slot_ms: params.slot_ms,
        },
      },
      headers: ACCEPT_JSON,
    }),
  );
}

export function fetchSample(
  cluster: string,
  topic: string,
  n = 5,
  partition = -1,
): Promise<SampleResponse> {
  return call(
    client.GET("/api/v1/clusters/{cluster}/topics/{topic}/sample", {
      params: { path: { cluster, topic }, query: { n, partition } },
      headers: ACCEPT_JSON,
    }),
  );
}

// Threshold above which produce bodies are gzip-compressed before being
// sent. Kept well under maxProduceBodyBytes-scale payloads so compression
// only kicks in where it actually pays off; small produce requests aren't
// worth the CompressionStream round trip.
const GZIP_PRODUCE_THRESHOLD_BYTES = 256 * 1024;

/**
 * Gzip-compresses a produce request body when the browser supports the
 * (widely available) CompressionStream API and the payload is large enough
 * to benefit — mainly the base64-encoded full value recovered for a
 * truncated message replay. This only reduces bytes actually transferred;
 * the server still enforces the same decompressed-size cap either way (see
 * internal/server/messages.go's maxProduceBodyBytes), so compression cannot
 * be used to sneak a too-large value past the limit.
 *
 * Falls back to sending the body uncompressed — without Content-Encoding —
 * whenever CompressionStream is unavailable or compression fails for any
 * reason; correctness never depends on this succeeding.
 */
async function maybeGzipBody(
  json: string,
): Promise<{ body: BodyInit; headers: Record<string, string> }> {
  if (json.length < GZIP_PRODUCE_THRESHOLD_BYTES || typeof CompressionStream === "undefined") {
    return { body: json, headers: {} };
  }
  try {
    const stream = new Blob([json]).stream().pipeThrough(new CompressionStream("gzip"));
    const compressed = await new Response(stream).blob();
    return { body: compressed, headers: { "Content-Encoding": "gzip" } };
  } catch {
    return { body: json, headers: {} };
  }
}

export async function produceMessage(
  cluster: string,
  topic: string,
  req: ProduceRequest,
  confirmProd = false,
): Promise<ProduceResult> {
  const { body, headers } = await maybeGzipBody(JSON.stringify(req));
  return call(
    client.POST("/api/v1/clusters/{cluster}/topics/{topic}/messages", {
      params: withProdConfirm({ path: { cluster, topic } }, confirmProd),
      body: req,
      bodySerializer: () => body,
      headers,
    }),
  );
}

// ---------------------------------------------------------------------------
// Bulk message copy
// ---------------------------------------------------------------------------

/**
 * copyMessages starts a server-side copy from `cluster`/`topic` to the
 * destination configured in `req`. Progress is reported via SSE; the returned
 * function aborts the stream when called.
 *
 * @param onProgress callback invoked for each SSE event
 * @returns abort function
 */
export function copyMessages(
  cluster: string,
  topic: string,
  req: CopyRequest,
  confirmProd: boolean,
  onProgress: (ev: CopyProgressEvent) => void,
): () => void {
  const ctrl = new AbortController();

  (async () => {
    let stream: ReadableStream<Uint8Array> | null | undefined;
    try {
      const res = await client.POST("/api/v1/clusters/{cluster}/topics/{topic}/copy", {
        params: withProdConfirm({ path: { cluster, topic } }, confirmProd),
        body: req,
        parseAs: "stream",
        signal: ctrl.signal,
      });
      stream = res.data;
    } catch (e) {
      if (e instanceof HttpError) {
        onProgress({ copied: 0, done: true, error: statusError(e).message });
      } else if (!(e instanceof DOMException && e.name === "AbortError")) {
        onProgress({ copied: 0, done: true, error: (e as Error).message });
      }
      return;
    }

    const reader = stream?.getReader();
    if (!reader) {
      onProgress({ copied: 0, done: true, error: "no response body" });
      return;
    }

    const decoder = new TextDecoder();
    let buf = "";
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split("\n");
        buf = lines.pop() ?? "";
        for (const line of lines) {
          if (line.startsWith("data: ")) {
            try {
              const ev = JSON.parse(line.slice(6)) as CopyProgressEvent;
              onProgress(ev);
            } catch {
              /* skip malformed */
            }
          }
        }
      }
    } catch (e) {
      if (!(e instanceof DOMException && e.name === "AbortError")) {
        onProgress({ copied: 0, done: true, error: (e as Error).message });
      }
    }
  })();

  return () => ctrl.abort();
}

export async function fetchGroups(cluster: string): Promise<GroupInfo[]> {
  const r = await call(
    client.GET("/api/v1/clusters/{cluster}/groups", {
      params: { path: { cluster } },
      headers: ACCEPT_JSON,
    }),
  );
  return r.groups ?? [];
}

export function fetchGroupDetail(cluster: string, group: string): Promise<GroupDetail> {
  return call(
    client.GET("/api/v1/clusters/{cluster}/groups/{group}", {
      params: { path: { cluster, group } },
      headers: ACCEPT_JSON,
    }),
  );
}

// --- Admin operations --------------------------------------------------------

export function resetGroupOffsets(
  cluster: string,
  group: string,
  req: ResetOffsetsRequest,
  confirmProd = false,
): Promise<ResetOffsetsResult> {
  return call(
    client.POST("/api/v1/clusters/{cluster}/groups/{group}/reset-offsets", {
      params: withProdConfirm({ path: { cluster, group } }, confirmProd),
      body: req,
    }),
  );
}

export function createGroup(cluster: string, req: CreateGroupRequest): Promise<ResetOffsetsResult> {
  return call(
    client.POST("/api/v1/clusters/{cluster}/groups", {
      params: { path: { cluster } },
      body: req,
    }),
  );
}

export function deleteGroup(
  cluster: string,
  group: string,
): Promise<Schemas["DeletedNameResponse"]> {
  return call(
    client.DELETE("/api/v1/clusters/{cluster}/groups/{group}", {
      params: { path: { cluster, group } },
    }),
  );
}

export function createTopic(
  cluster: string,
  req: CreateTopicRequest,
): Promise<Schemas["CreatedResponse"]> {
  return call(
    client.POST("/api/v1/clusters/{cluster}/topics", {
      params: { path: { cluster } },
      body: req,
    }),
  );
}

// --- Schema Registry ---

export async function listSubjects(cluster: string): Promise<Subject[]> {
  const data = await call(
    client.GET("/api/v1/clusters/{cluster}/schemas/subjects", {
      params: { path: { cluster } },
    }),
    bodyError,
  );
  return data.subjects ?? [];
}

export function getSchemaVersion(
  cluster: string,
  subject: string,
  version: string | number,
): Promise<SchemaVersion> {
  return call(
    client.GET("/api/v1/clusters/{cluster}/schemas/subjects/{subject}/versions/{version}", {
      params: { path: { cluster, subject, version: String(version) } },
    }),
    bodyError,
  );
}

export function deleteSubject(
  cluster: string,
  subject: string,
  permanent = false,
): Promise<Schemas["DeleteSubjectResponse"]> {
  return call(
    client.DELETE("/api/v1/clusters/{cluster}/schemas/subjects/{subject}", {
      params: {
        path: { cluster, subject },
        query: { permanent: permanent ? true : undefined },
      },
    }),
  );
}

// --- Alter topic configs ---
export function alterTopicConfigs(
  cluster: string,
  topic: string,
  req: AlterTopicConfigsRequest,
): Promise<Schemas["AlterTopicConfigsResponse"]> {
  return call(
    client.PATCH("/api/v1/clusters/{cluster}/topics/{topic}/configs", {
      params: { path: { cluster, topic } },
      body: req,
    }),
  );
}

// --- ACLs ---
export async function listACLs(cluster: string): Promise<ACLEntry[]> {
  const data = await call(
    client.GET("/api/v1/clusters/{cluster}/acls", { params: { path: { cluster } } }),
    bodyError,
  );
  return data.acls ?? [];
}

export async function createACL(cluster: string, spec: ACLSpec): Promise<void> {
  await call(
    client.POST("/api/v1/clusters/{cluster}/acls", {
      params: { path: { cluster } },
      body: spec,
      parseAs: "text",
    }),
    bodyError,
  );
}

export async function deleteACL(cluster: string, spec: ACLSpec): Promise<number> {
  const data = await call(
    client.DELETE("/api/v1/clusters/{cluster}/acls", {
      params: { path: { cluster } },
      body: spec,
    }),
    bodyError,
  );
  return data.deleted ?? 0;
}

export async function searchMessages(
  cluster: string,
  topic: string,
  req: SearchRequest,
): Promise<SearchResponse> {
  const data = await call(
    client.POST("/api/v1/clusters/{cluster}/topics/{topic}/messages/search", {
      params: { path: { cluster, topic } },
      body: req,
    }),
    bodyOrStatusTextError,
  );
  return { ...data, messages: data.messages ?? [] };
}

// --- SCRAM users ---
export async function listSCRAMUsers(cluster: string): Promise<SCRAMUser[]> {
  const data = await call(
    client.GET("/api/v1/clusters/{cluster}/users", { params: { path: { cluster } } }),
    bodyError,
  );
  return data.users ?? [];
}
export async function upsertSCRAMUser(
  cluster: string,
  req: Schemas["UpsertSCRAMUserRequest"],
): Promise<void> {
  await call(
    client.POST("/api/v1/clusters/{cluster}/users", {
      params: { path: { cluster } },
      body: req,
      parseAs: "text",
    }),
    bodyError,
  );
}
export async function deleteSCRAMUser(
  cluster: string,
  user: string,
  mechanism?: string,
): Promise<void> {
  await call(
    client.DELETE("/api/v1/clusters/{cluster}/users/{user}", {
      params: {
        path: { cluster, user },
        // Callers pass the mechanism as listed by the server.
        query: { mechanism: (mechanism || undefined) as SCRAMMechanism | undefined },
      },
      parseAs: "text",
    }),
    bodyError,
  );
}

// --- RBAC / Me ---

/** Returns true if the user has the given action on resourceType. */
export function can(
  me: { rbac_enabled: boolean; permissions: Record<string, string[]> } | undefined,
  _clusterName: string,
  resourceType: string,
  action: string,
  _resourceName?: string,
): boolean {
  if (!me) return true;
  if (!me.rbac_enabled) return true;
  const perms = me.permissions ?? {};
  const allPerms = perms["*"];
  if (allPerms && (allPerms.includes("*") || allPerms.includes(action))) return true;
  const typePerms = perms[resourceType];
  if (!typePerms) return false;
  return typePerms.includes("*") || typePerms.includes(action);
}

// --- Private cluster connection test ---------------------------------------

/**
 * Probes a private-cluster config by POSTing it to /clusters/_test. The
 * backend opens a short-lived kgo client, describes a handful of topics, and
 * returns ClusterInfo. Does not require the cluster to be saved.
 */
export async function testCluster(cfg: PrivateCluster): Promise<ClusterInfo> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 20_000);
  try {
    return await call(
      client.POST("/api/v1/clusters/_test", {
        body: toBackendClusterConfig(cfg),
        signal: controller.signal,
      }),
    );
  } catch (e) {
    if ((e as { name?: string }).name === "AbortError") {
      throw new Error("timed out probing brokers — try again, broker DNS may be cold");
    }
    throw e;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Downloads the full raw value of a single Kafka record identified by
 * partition and offset. Triggers a browser file-save dialog.
 * Throws on HTTP error: RawValueTooLargeError on 413 (value exceeds the
 * server cap), RawValueMaskedError on 403 `value_masked`.
 */
export async function downloadMessageRaw(
  cluster: string,
  topic: string,
  partition: number,
  offset: number,
): Promise<void> {
  const { data: blob, response } = await send(
    client.GET("/api/v1/clusters/{cluster}/topics/{topic}/messages/{partition}/{offset}/raw", {
      params: { path: { cluster, topic, partition, offset } },
      parseAs: "blob",
    }),
    rawValueError,
  );
  const disposition = response.headers.get("content-disposition") ?? "";
  const match = disposition.match(/filename="([^"]+)"/);
  const filename = match ? match[1] : `${topic}-p${partition}-o${offset}.bin`;
  const url = URL.createObjectURL(blob as Blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

/** Thrown by the raw-value fetchers when the value exceeds the server's raw-download cap (HTTP 413). */
export class RawValueTooLargeError extends Error {}

/**
 * Thrown by the raw-value fetchers when the server refuses the value because
 * the cluster's data masking rules change it (HTTP 403 `value_masked`). The
 * message is the server's own text.
 */
export class RawValueMaskedError extends Error {}

function rawValueError(e: HttpError): Error {
  const body = e.json() ?? {};
  if (e.status === 403 && body.code === "value_masked") {
    return new RawValueMaskedError(body.error ?? "value is masked and cannot be downloaded");
  }
  const detail = body.error ? `: ${body.error}` : "";
  if (e.status === 413) return new RawValueTooLargeError(`HTTP 413${detail}`);
  return new Error(`HTTP ${e.status}${detail}`);
}

function arrayBufferToBase64(buf: ArrayBuffer): string {
  // Chunked to avoid blowing the call stack on String.fromCharCode(...bytes)
  // for multi-megabyte payloads.
  const bytes = new Uint8Array(buf);
  const chunkSize = 0x8000;
  let binary = "";
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

/**
 * Decodes a standard-base64 string (as returned by fetchMessageRawBase64)
 * into a UTF-8 text string. Goes through raw bytes rather than a plain
 * `atob()` + string cast so multi-byte UTF-8 characters decode correctly.
 */
export function base64ToUtf8(b64: string): string {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return new TextDecoder("utf-8").decode(bytes);
}

/**
 * Fetches the full raw value of a single Kafka record identified by
 * partition and offset, returning it as a base64 string (rather than
 * triggering a file download like downloadMessageRaw). Used by the Replay
 * dialog to recover the untruncated bytes of a value that was cut to 64 KB
 * for the message list preview. Throws RawValueTooLargeError on HTTP 413,
 * RawValueMaskedError on HTTP 403 `value_masked`, a plain Error on any other
 * HTTP error.
 */
export async function fetchMessageRawBase64(
  cluster: string,
  topic: string,
  partition: number,
  offset: number,
  signal?: AbortSignal,
): Promise<string> {
  const buf = await call(
    client.GET("/api/v1/clusters/{cluster}/topics/{topic}/messages/{partition}/{offset}/raw", {
      params: { path: { cluster, topic, partition, offset } },
      parseAs: "arrayBuffer",
      signal,
    }),
    rawValueError,
  );
  return arrayBufferToBase64(buf as ArrayBuffer);
}
