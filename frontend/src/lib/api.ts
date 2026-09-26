import { apiFetch } from "../auth/api";
import { clusterPath, fetchAPI } from "./api-http";
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
export type ApiError = Schemas["Error"];
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

async function getJSON<T>(path: string): Promise<T> {
  const res = await apiFetch(path, { headers: { Accept: "application/json" } });
  if (!res.ok) {
    let detail = "";
    try {
      const body = (await res.json()) as Partial<ApiError>;
      detail = body.error ? `: ${body.error}` : "";
    } catch {
      /* ignore */
    }
    throw new Error(`HTTP ${res.status}${detail}`);
  }
  return (await res.json()) as T;
}

async function getJSONForCluster<T>(cluster: string, path: string): Promise<T> {
  const res = await fetchAPI(cluster, path, {
    headers: { Accept: "application/json" },
  });
  if (!res.ok) {
    let detail = "";
    try {
      const body = (await res.json()) as Partial<ApiError>;
      detail = body.error ? `: ${body.error}` : "";
    } catch {
      /* ignore */
    }
    throw new Error(`HTTP ${res.status}${detail}`);
  }
  return (await res.json()) as T;
}

export function fetchInfo(): Promise<InfoResponse> {
  return getJSON<InfoResponse>("/api/v1/info");
}

export async function fetchClusters(): Promise<ClusterInfo[]> {
  const r = await getJSON<Schemas["ListClustersResponse"]>("/api/v1/clusters");
  return r.clusters;
}

export async function fetchTopics(cluster: string): Promise<TopicInfo[]> {
  const r = await getJSONForCluster<Schemas["ListTopicsResponse"]>(
    cluster,
    clusterPath(cluster, `/topics`),
  );
  return r.topics;
}

export async function fetchBrokers(cluster: string): Promise<BrokerInfo[]> {
  const r = await getJSONForCluster<Schemas["ListBrokersResponse"]>(
    cluster,
    clusterPath(cluster, `/brokers`),
  );
  return r.brokers;
}

export async function fetchTopicDetail(cluster: string, topic: string): Promise<TopicDetail> {
  const r = await getJSONForCluster<Schemas["DescribeTopicResponse"]>(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}`),
  );
  return r.topic;
}

export async function fetchTopicConsumers(
  cluster: string,
  topic: string,
): Promise<TopicConsumer[]> {
  const r = await getJSONForCluster<Schemas["ListTopicConsumersResponse"]>(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/consumers`),
  );
  return r.consumers ?? [];
}

export async function fetchMessages(
  cluster: string,
  topic: string,
  params: ConsumeParams = {},
): Promise<MessagesPage> {
  const qs = new URLSearchParams();
  if (params.partition !== undefined && params.partition >= 0)
    qs.set("partition", String(params.partition));
  if (params.limit !== undefined) qs.set("limit", String(params.limit));
  if (params.from) qs.set("from", params.from);
  if (params.offset !== undefined) qs.set("offset", String(params.offset));
  if (params.partitionOffsets) {
    const pairs = Object.entries(params.partitionOffsets)
      .map(([p, o]) => `${p}:${o}`)
      .join(",");
    if (pairs) qs.set("partition_offsets", pairs);
  }
  if (params.from_ts_ms !== undefined) qs.set("from_ts_ms", String(params.from_ts_ms));
  if (params.to_ts_ms !== undefined) qs.set("to_ts_ms", String(params.to_ts_ms));
  if (params.cursor) qs.set("cursor", params.cursor);
  const q = qs.toString();
  return await getJSONForCluster<MessagesPage>(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/messages${q ? "?" + q : ""}`),
  );
}

export async function fetchMessageCount(
  cluster: string,
  topic: string,
  params: Pick<ConsumeParams, "partition" | "from_ts_ms" | "to_ts_ms"> = {},
): Promise<MessageCountResponse> {
  const qs = new URLSearchParams();
  if (params.partition !== undefined && params.partition >= 0)
    qs.set("partition", String(params.partition));
  if (params.from_ts_ms !== undefined) qs.set("from_ts_ms", String(params.from_ts_ms));
  if (params.to_ts_ms !== undefined) qs.set("to_ts_ms", String(params.to_ts_ms));
  const q = qs.toString();
  return await getJSONForCluster<MessageCountResponse>(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/messages/count${q ? "?" + q : ""}`),
  );
}

export async function fetchMessageTimeline(
  cluster: string,
  topic: string,
  params: { partition?: number; from_ts_ms: number; to_ts_ms: number; slot_ms: number },
): Promise<MessageTimelineResponse> {
  const qs = new URLSearchParams();
  if (params.partition !== undefined && params.partition >= 0)
    qs.set("partition", String(params.partition));
  qs.set("from_ts_ms", String(params.from_ts_ms));
  qs.set("to_ts_ms", String(params.to_ts_ms));
  qs.set("slot_ms", String(params.slot_ms));
  return await getJSONForCluster<MessageTimelineResponse>(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/messages/timeline?${qs.toString()}`),
  );
}

export async function fetchSample(
  cluster: string,
  topic: string,
  n = 5,
  partition = -1,
): Promise<SampleResponse> {
  const qs = new URLSearchParams();
  qs.set("n", String(n));
  qs.set("partition", String(partition));
  return await getJSONForCluster<SampleResponse>(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/sample?${qs}`),
  );
}

// PROD_CONFIRM_HEADER must match internal/server/prod_confirm.go's
// ProdConfirmHeader. Sent only after the user confirms the production
// warning dialog; the backend is the actual enforcement point — it rejects
// mutating calls against is_prod clusters that omit this header, regardless
// of what this client's local cluster list believes.
const PROD_CONFIRM_HEADER = "X-Kafkito-Confirm-Prod";

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
  const json = JSON.stringify(req);
  const { body, headers } = await maybeGzipBody(json);
  const res = await fetchAPI(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/messages`),
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...headers,
        ...(confirmProd ? { [PROD_CONFIRM_HEADER]: "true" } : {}),
      },
      body,
    },
  );
  if (!res.ok) {
    let detail = "";
    try {
      const b = (await res.json()) as Partial<ApiError>;
      detail = b.error ? `: ${b.error}` : "";
    } catch {
      /* ignore */
    }
    throw new Error(`HTTP ${res.status}${detail}`);
  }
  return (await res.json()) as ProduceResult;
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
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (confirmProd) headers[PROD_CONFIRM_HEADER] = "true";

    let res: Response;
    try {
      res = await fetchAPI(
        cluster,
        clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/copy`),
        {
          method: "POST",
          headers,
          body: JSON.stringify(req),
          signal: ctrl.signal,
        },
      );
    } catch (e) {
      if (!(e instanceof DOMException && e.name === "AbortError")) {
        onProgress({ copied: 0, done: true, error: (e as Error).message });
      }
      return;
    }

    if (!res.ok) {
      let detail = "";
      try {
        const b = (await res.json()) as Partial<ApiError>;
        detail = b.error ? `: ${b.error}` : "";
      } catch {
        /* ignore */
      }
      onProgress({ copied: 0, done: true, error: `HTTP ${res.status}${detail}` });
      return;
    }

    const reader = res.body?.getReader();
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
  const r = await getJSONForCluster<Schemas["ListGroupsResponse"]>(
    cluster,
    clusterPath(cluster, `/groups`),
  );
  return r.groups ?? [];
}

export async function fetchGroupDetail(cluster: string, group: string): Promise<GroupDetail> {
  return getJSONForCluster<GroupDetail>(
    cluster,
    clusterPath(cluster, `/groups/${encodeURIComponent(group)}`),
  );
}

// --- Admin operations --------------------------------------------------------

async function sendJSONForCluster<T>(
  cluster: string | null,
  path: string,
  method: string,
  body?: unknown,
  confirmProd = false,
): Promise<T> {
  const init: RequestInit = {
    method,
    headers: {
      ...(body ? { "Content-Type": "application/json" } : {}),
      ...(confirmProd ? { [PROD_CONFIRM_HEADER]: "true" } : {}),
    },
  };
  if (body !== undefined) init.body = JSON.stringify(body);
  const res = await fetchAPI(cluster, path, init);
  if (!res.ok) {
    let detail = "";
    try {
      const b = (await res.json()) as Partial<ApiError>;
      detail = b.error ? `: ${b.error}` : "";
    } catch {
      /* ignore */
    }
    throw new Error(`HTTP ${res.status}${detail}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export function resetGroupOffsets(
  cluster: string,
  group: string,
  req: ResetOffsetsRequest,
  confirmProd = false,
): Promise<ResetOffsetsResult> {
  return sendJSONForCluster<ResetOffsetsResult>(
    cluster,
    clusterPath(cluster, `/groups/${encodeURIComponent(group)}/reset-offsets`),
    "POST",
    req,
    confirmProd,
  );
}

export function createGroup(cluster: string, req: CreateGroupRequest): Promise<ResetOffsetsResult> {
  return sendJSONForCluster<ResetOffsetsResult>(
    cluster,
    clusterPath(cluster, `/groups`),
    "POST",
    req,
  );
}

export function deleteGroup(
  cluster: string,
  group: string,
): Promise<Schemas["DeletedNameResponse"]> {
  return sendJSONForCluster<Schemas["DeletedNameResponse"]>(
    cluster,
    clusterPath(cluster, `/groups/${encodeURIComponent(group)}`),
    "DELETE",
  );
}

export function createTopic(
  cluster: string,
  req: CreateTopicRequest,
): Promise<Schemas["CreatedResponse"]> {
  return sendJSONForCluster<Schemas["CreatedResponse"]>(
    cluster,
    clusterPath(cluster, `/topics`),
    "POST",
    req,
  );
}

// --- Schema Registry ---

export async function listSubjects(cluster: string): Promise<Subject[]> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/schemas/subjects`));
  if (!r.ok) throw new Error(await r.text());
  const data = (await r.json()) as Schemas["ListSubjectsResponse"];
  return data.subjects ?? [];
}

export async function getSchemaVersion(
  cluster: string,
  subject: string,
  version: string | number,
): Promise<SchemaVersion> {
  const r = await fetchAPI(
    cluster,
    clusterPath(
      cluster,
      `/schemas/subjects/${encodeURIComponent(subject)}/versions/${encodeURIComponent(String(version))}`,
    ),
  );
  if (!r.ok) throw new Error(await r.text());
  return (await r.json()) as SchemaVersion;
}

export function deleteSubject(
  cluster: string,
  subject: string,
  permanent = false,
): Promise<Schemas["DeleteSubjectResponse"]> {
  return sendJSONForCluster<Schemas["DeleteSubjectResponse"]>(
    cluster,
    clusterPath(
      cluster,
      `/schemas/subjects/${encodeURIComponent(subject)}${permanent ? "?permanent=true" : ""}`,
    ),
    "DELETE",
  );
}

// --- Alter topic configs ---
export function alterTopicConfigs(
  cluster: string,
  topic: string,
  req: AlterTopicConfigsRequest,
): Promise<Schemas["AlterTopicConfigsResponse"]> {
  return sendJSONForCluster<Schemas["AlterTopicConfigsResponse"]>(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/configs`),
    "PATCH",
    req,
  );
}

// --- ACLs ---
export async function listACLs(cluster: string): Promise<ACLEntry[]> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/acls`));
  if (!r.ok) throw new Error(await r.text());
  const data = (await r.json()) as Schemas["ListACLsResponse"];
  return data.acls ?? [];
}

export async function createACL(cluster: string, spec: ACLSpec): Promise<void> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/acls`), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(spec),
  });
  if (!r.ok) {
    const t = await r.text();
    try {
      throw new Error((JSON.parse(t) as Partial<ApiError>).error ?? t);
    } catch {
      throw new Error(t);
    }
  }
}

export async function deleteACL(cluster: string, spec: ACLSpec): Promise<number> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/acls`), {
    method: "DELETE",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(spec),
  });
  if (!r.ok) {
    const t = await r.text();
    try {
      throw new Error((JSON.parse(t) as Partial<ApiError>).error ?? t);
    } catch {
      throw new Error(t);
    }
  }
  const data = (await r.json()) as Schemas["DeleteACLResponse"];
  return data.deleted ?? 0;
}

export async function searchMessages(
  cluster: string,
  topic: string,
  req: SearchRequest,
): Promise<SearchResponse> {
  const r = await fetchAPI(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/messages/search`),
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(req),
    },
  );
  if (!r.ok) {
    const txt = await r.text();
    throw new Error(txt || r.statusText);
  }
  const data = (await r.json()) as SearchResponse;
  return { ...data, messages: data.messages ?? [] };
}

// --- SCRAM users ---
export async function listSCRAMUsers(cluster: string): Promise<SCRAMUser[]> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/users`));
  if (!r.ok) throw new Error(await r.text());
  const data = (await r.json()) as Schemas["ListSCRAMUsersResponse"];
  return data.users ?? [];
}
export async function upsertSCRAMUser(
  cluster: string,
  req: Schemas["UpsertSCRAMUserRequest"],
): Promise<void> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/users`), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(req),
  });
  if (!r.ok) {
    const t = await r.text();
    try {
      throw new Error((JSON.parse(t) as Partial<ApiError>).error ?? t);
    } catch {
      throw new Error(t);
    }
  }
}
export async function deleteSCRAMUser(
  cluster: string,
  user: string,
  mechanism?: string,
): Promise<void> {
  const qs = mechanism ? `?mechanism=${encodeURIComponent(mechanism)}` : "";
  const r = await fetchAPI(
    cluster,
    clusterPath(cluster, `/users/${encodeURIComponent(user)}${qs}`),
    { method: "DELETE" },
  );
  if (!r.ok) {
    const t = await r.text();
    try {
      throw new Error((JSON.parse(t) as Partial<ApiError>).error ?? t);
    } catch {
      throw new Error(t);
    }
  }
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
  let res: Response;
  try {
    res = await apiFetch("/api/v1/clusters/_test", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(toBackendClusterConfig(cfg)),
      signal: controller.signal,
    });
  } catch (e) {
    if ((e as { name?: string }).name === "AbortError") {
      throw new Error("timed out probing brokers — try again, broker DNS may be cold");
    }
    throw e;
  } finally {
    clearTimeout(timer);
  }
  if (!res.ok) {
    let detail = "";
    try {
      const b = (await res.json()) as Partial<ApiError>;
      detail = b.error ? `: ${b.error}` : "";
    } catch {
      /* ignore */
    }
    throw new Error(`HTTP ${res.status}${detail}`);
  }
  return (await res.json()) as ClusterInfo;
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
  const path = clusterPath(
    cluster,
    `topics/${encodeURIComponent(topic)}/messages/${partition}/${offset}/raw`,
  );
  const res = await fetchAPI(cluster, path);
  if (!res.ok) throw await rawValueError(res);
  const disposition = res.headers.get("content-disposition") ?? "";
  const match = disposition.match(/filename="([^"]+)"/);
  const filename = match ? match[1] : `${topic}-p${partition}-o${offset}.bin`;
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
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

async function rawValueError(res: Response): Promise<Error> {
  let body: Partial<ApiError> = {};
  try {
    body = (await res.json()) as Partial<ApiError>;
  } catch {
    /* ignore */
  }
  if (res.status === 403 && body.code === "value_masked") {
    return new RawValueMaskedError(body.error ?? "value is masked and cannot be downloaded");
  }
  const detail = body.error ? `: ${body.error}` : "";
  if (res.status === 413) return new RawValueTooLargeError(`HTTP 413${detail}`);
  return new Error(`HTTP ${res.status}${detail}`);
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
  const path = clusterPath(
    cluster,
    `topics/${encodeURIComponent(topic)}/messages/${partition}/${offset}/raw`,
  );
  const res = await fetchAPI(cluster, path, { signal });
  if (!res.ok) throw await rawValueError(res);
  return arrayBufferToBase64(await res.arrayBuffer());
}
