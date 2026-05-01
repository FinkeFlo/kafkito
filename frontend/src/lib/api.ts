import { apiFetch } from "../auth/api";
import { clusterPath, fetchAPI } from "./api-http";
import type { PrivateCluster } from "./private-clusters";
import { toBackendClusterConfig } from "./private-clusters";

export interface InfoResponse {
  name: string;
  version: string;
  commit?: string;
  built_at?: string;
}

export interface Capabilities {
  describe_cluster: boolean;
  list_topics: boolean;
  describe_configs: boolean;
  list_groups: boolean;
  create_topic: boolean;
  delete_topic: boolean;
  alter_configs: boolean;
  errors?: Record<string, string>;
  probed_at?: string;
}

export interface ClusterInfo {
  name: string;
  reachable: boolean;
  error?: string;
  auth_type: string;
  tls: boolean;
  schema_registry: boolean;
  capabilities?: Capabilities;
  // Aggregate counters and metrics (filled best-effort by the background
  // metrics collector; undefined when not yet known or the cluster is
  // unreachable).
  brokers?: number;
  topics?: number;
  groups?: number;
  total_messages?: number;
  total_lag?: number;
  total_rate_per_sec?: number;
}

export interface TopicInfo {
  name: string;
  partitions: number;
  replication_factor: number;
  is_internal: boolean;
  // Metric fields filled best-effort. `retention_ms` of -1 means infinite.
  messages?: number;
  size_bytes?: number;
  retention_ms?: number;
  rate_per_sec?: number;
  lag?: number;
}

export interface PartitionInfo {
  partition: number;
  leader: number;
  replicas: number[];
  isr: number[];
  start_offset: number;
  end_offset: number;
  messages: number;
}

export interface TopicConfigEntry {
  name: string;
  value: string;
  is_default: boolean;
  source?: string;
  sensitive: boolean;
}

export interface TopicDetail {
  name: string;
  is_internal: boolean;
  partitions: PartitionInfo[];
  replication_factor: number;
  messages: number;
  configs: TopicConfigEntry[];
  size_bytes?: number;
}

export interface SRDecodedMeta {
  format?: string;
  schema_id?: number;
  subject?: string;
  version?: number;
}

export interface Message {
  partition: number;
  offset: number;
  timestamp_ms: number;
  key?: string;
  key_encoding: string;
  key_b64?: string;
  value?: string;
  value_encoding: string;
  value_b64?: string;
  headers?: Record<string, string>;
  masked?: boolean;
  key_sr?: SRDecodedMeta;
  value_sr?: SRDecodedMeta;
}

export interface ConsumeParams {
  partition?: number;
  limit?: number;
  from?: "end" | "start" | "offset";
  offset?: number;
  from_ts_ms?: number;
  to_ts_ms?: number;
  cursor?: string;
}

export interface MessagesPage {
  messages: Message[];
  has_more?: boolean;
  next_cursor?: string;
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await apiFetch(path, { headers: { Accept: "application/json" } });
  if (!res.ok) {
    let detail = "";
    try {
      const body = (await res.json()) as { error?: string };
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
      const body = (await res.json()) as { error?: string };
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
  const r = await getJSON<{ clusters: ClusterInfo[] }>("/api/v1/clusters");
  return r.clusters;
}

export async function fetchTopics(cluster: string): Promise<TopicInfo[]> {
  const r = await getJSONForCluster<{ topics: TopicInfo[] }>(cluster, clusterPath(cluster, `/topics`),
  );
  return r.topics;
}

export interface BrokerInfo {
  node_id: number;
  host: string;
  port: number;
  rack?: string;
  is_controller: boolean;
}

export async function fetchBrokers(cluster: string): Promise<BrokerInfo[]> {
  const r = await getJSONForCluster<{ brokers: BrokerInfo[] }>(
    cluster,
    clusterPath(cluster, `/brokers`),
  );
  return r.brokers;
}

export async function fetchTopicDetail(
  cluster: string,
  topic: string,
): Promise<TopicDetail> {
  const r = await getJSONForCluster<{ topic: TopicDetail }>(cluster, clusterPath(cluster, `/topics/${encodeURIComponent(topic)}`),
  );
  return r.topic;
}

export interface TopicConsumer {
  group_id: string;
  state: string;
  members: number;
  partitions_assigned: number[];
  lag: number;
  lag_known: boolean;
  error?: string;
}

export async function fetchTopicConsumers(
  cluster: string,
  topic: string,
): Promise<TopicConsumer[]> {
  const r = await getJSONForCluster<{ consumers: TopicConsumer[] }>(cluster, clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/consumers`),
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
  if (params.from_ts_ms !== undefined) qs.set("from_ts_ms", String(params.from_ts_ms));
  if (params.to_ts_ms !== undefined) qs.set("to_ts_ms", String(params.to_ts_ms));
  if (params.cursor) qs.set("cursor", params.cursor);
  const q = qs.toString();
  return await getJSONForCluster<MessagesPage>(
    cluster,
    clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/messages${q ? "?" + q : ""}`),
  );
}

export interface SampleResponse {
  cluster: string;
  topic: string;
  messages: Message[];
  sampled_at: number;
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

export interface ProduceRequest {
  partition?: number;
  key: string;
  value: string;
  key_encoding?: "text" | "base64";
  value_encoding?: "text" | "base64";
  headers?: Record<string, string>;
}

export interface ProduceResult {
  topic: string;
  partition: number;
  offset: number;
  timestamp_ms: number;
}

export async function produceMessage(
  cluster: string,
  topic: string,
  req: ProduceRequest,
): Promise<ProduceResult> {
  const res = await fetchAPI(cluster, clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/messages`),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    },
  );
  if (!res.ok) {
    let detail = "";
    try {
      const b = (await res.json()) as { error?: string };
      detail = b.error ? `: ${b.error}` : "";
    } catch {
      /* ignore */
    }
    throw new Error(`HTTP ${res.status}${detail}`);
  }
  return (await res.json()) as ProduceResult;
}

export interface GroupInfo {
  group_id: string;
  state: string;
  protocol_type: string;
  protocol?: string;
  coordinator_id: number;
  members: number;
  topics?: number;
  lag: number;
  lag_known: boolean;
  error?: string;
}

export interface MemberAssignment {
  topic: string;
  partitions: number[];
}

export interface GroupMember {
  member_id: string;
  instance_id?: string;
  client_id: string;
  client_host: string;
  assignments: MemberAssignment[];
}

export interface GroupOffset {
  topic: string;
  partition: number;
  offset: number;
  log_end: number;
  lag: number;
  metadata?: string;
  assigned_to?: string;
}

export interface GroupDetail extends Omit<GroupInfo, "members"> {
  members: GroupMember[];
  offsets: GroupOffset[];
}

export async function fetchGroups(cluster: string): Promise<GroupInfo[]> {
  const r = await getJSONForCluster<{ groups: GroupInfo[] }>(cluster, clusterPath(cluster, `/groups`),
  );
  return r.groups ?? [];
}

export async function fetchGroupDetail(
  cluster: string,
  group: string,
): Promise<GroupDetail> {
  return getJSONForCluster<GroupDetail>(cluster, clusterPath(cluster, `/groups/${encodeURIComponent(group)}`),
  );
}

// --- Admin operations --------------------------------------------------------

async function sendJSONForCluster<T>(
  cluster: string | null,
  path: string,
  method: string,
  body?: unknown,
): Promise<T> {
  const init: RequestInit = {
    method,
    headers: body ? { "Content-Type": "application/json" } : {},
  };
  if (body !== undefined) init.body = JSON.stringify(body);
  const res = await fetchAPI(cluster, path, init);
  if (!res.ok) {
    let detail = "";
    try {
      const b = (await res.json()) as { error?: string };
      detail = b.error ? `: ${b.error}` : "";
    } catch {
      /* ignore */
    }
    throw new Error(`HTTP ${res.status}${detail}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export type ResetStrategy =
  | "earliest"
  | "latest"
  | "offset"
  | "timestamp"
  | "shift-by";

export interface ResetOffsetsRequest {
  topic: string;
  partitions?: number[];
  strategy: ResetStrategy;
  offset?: number;
  timestamp_ms?: number;
  shift?: number;
  dry_run?: boolean;
}

export interface ResetOffsetResult {
  partition: number;
  old_offset: number;
  new_offset: number;
  error?: string;
}

export interface ResetOffsetsResult {
  group: string;
  topic: string;
  dry_run: boolean;
  results: ResetOffsetResult[];
}

export function resetGroupOffsets(
  cluster: string,
  group: string,
  req: ResetOffsetsRequest,
): Promise<ResetOffsetsResult> {
  return sendJSONForCluster<ResetOffsetsResult>(cluster, clusterPath(cluster, `/groups/${encodeURIComponent(group)}/reset-offsets`),
    "POST",
    req,
  );
}

export function deleteGroup(cluster: string, group: string): Promise<void> {
  return sendJSONForCluster<void>(cluster, clusterPath(cluster, `/groups/${encodeURIComponent(group)}`),
    "DELETE",
  );
}

export interface CreateTopicRequest {
  name: string;
  partitions: number;
  replication_factor: number;
  configs?: Record<string, string>;
}

export function createTopic(
  cluster: string,
  req: CreateTopicRequest,
): Promise<void> {
  return sendJSONForCluster<void>(cluster, clusterPath(cluster, `/topics`),
    "POST",
    req,
  );
}

export function deleteTopic(cluster: string, topic: string): Promise<void> {
  return sendJSONForCluster<void>(cluster, clusterPath(cluster, `/topics/${encodeURIComponent(topic)}`),
    "DELETE",
  );
}

export interface DeleteRecordsResult {
  partition: number;
  low_watermark: number;
  requested_offset: number;
  error?: string;
}

export function deleteRecords(
  cluster: string,
  topic: string,
  partitions: Record<number, number>,
): Promise<{ results: DeleteRecordsResult[] }> {
  return sendJSONForCluster<{ results: DeleteRecordsResult[] }>(cluster, clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/records`),
    "DELETE",
    { partitions },
  );
}

// --- Schema Registry ---

export interface Subject {
  name: string;
  versions: number[];
  latest_schema_type?: string;
}

export interface SchemaReference {
  name: string;
  subject: string;
  version: number;
}

export interface SchemaVersion {
  subject: string;
  id: number;
  version: number;
  schemaType?: string;
  schema: string;
  references?: SchemaReference[];
  config?: { compatibilityLevel?: string };
}

export async function listSubjects(cluster: string): Promise<Subject[]> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/schemas/subjects`),
  );
  if (!r.ok) throw new Error(await r.text());
  const data = (await r.json()) as { subjects: Subject[] };
  return data.subjects ?? [];
}

export async function getSchemaVersion(
  cluster: string,
  subject: string,
  version: string | number,
): Promise<SchemaVersion> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/schemas/subjects/${encodeURIComponent(subject)}/versions/${encodeURIComponent(String(version))}`),
  );
  if (!r.ok) throw new Error(await r.text());
  return (await r.json()) as SchemaVersion;
}

export function deleteSubject(
  cluster: string,
  subject: string,
  permanent = false,
): Promise<void> {
  return sendJSONForCluster<void>(cluster, clusterPath(cluster, `/schemas/subjects/${encodeURIComponent(subject)}${permanent ? "?permanent=true" : ""}`),
    "DELETE",
  );
}

// --- Alter topic configs ---
export interface AlterTopicConfigsRequest {
  set?: Record<string, string>;
  delete?: string[];
}
export interface AlterTopicConfigsResult {
  name: string;
  op: "set" | "delete";
  value?: string;
  error?: string;
}
export function alterTopicConfigs(
  cluster: string,
  topic: string,
  req: AlterTopicConfigsRequest,
): Promise<{ results: AlterTopicConfigsResult[] }> {
  return sendJSONForCluster<{ results: AlterTopicConfigsResult[] }>(cluster, clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/configs`),
    "PATCH",
    req,
  );
}

// --- ACLs ---
export interface ACLEntry {
  principal: string;
  host: string;
  resource_type: string;
  resource_name: string;
  pattern_type: string;
  operation: string;
  permission_type: string;
}
export async function listACLs(cluster: string): Promise<ACLEntry[]> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/acls`));
  if (!r.ok) throw new Error(await r.text());
  const data = (await r.json()) as { acls?: ACLEntry[] };
  return data.acls ?? [];
}

export type ACLSpec = ACLEntry;

export async function createACL(cluster: string, spec: ACLSpec): Promise<void> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/acls`), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(spec),
  });
  if (!r.ok) {
    const t = await r.text();
    try {
      throw new Error((JSON.parse(t) as { error?: string }).error ?? t);
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
      throw new Error((JSON.parse(t) as { error?: string }).error ?? t);
    } catch {
      throw new Error(t);
    }
  }
  const data = (await r.json()) as { deleted?: number };
  return data.deleted ?? 0;
}

export type SearchMode = "contains" | "jsonpath" | "xpath" | "js";
export type SearchOp = "exists" | "eq" | "ne" | "contains" | "regex" | "gt" | "lt" | "gte" | "lte";
export type SearchZone = "value" | "key" | "headers";
export type SearchDirection = "newest_first" | "oldest_first";

export interface SearchRequest {
  partition?: number;
  limit?: number;
  budget?: number;
  direction?: SearchDirection;
  stop_on_limit?: boolean;
  mode?: SearchMode;
  path?: string;
  op?: SearchOp;
  value?: string;
  zones?: SearchZone[];
  from_ts_ms?: number;
  to_ts_ms?: number;
  cursors?: Record<string, number>;
}

export interface SearchStats {
  scanned: number;
  matched: number;
  budget_exhausted: boolean;
  direction: SearchDirection;
  next_cursors?: Record<string, number>;
  resolved_range?: Record<string, { start: number; end: number }>;
  parse_errors: number;
  durations_ms?: Record<string, number>;
}

export interface SearchResponse {
  messages: Message[] | null;
  search: SearchStats;
}

export async function searchMessages(
  cluster: string,
  topic: string,
  req: SearchRequest,
): Promise<SearchResponse> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/topics/${encodeURIComponent(topic)}/messages/search`),
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
  const data = (await r.json()) as { messages: Message[] | null; search: SearchStats };
  return { messages: data.messages ?? [], search: data.search };
}

// --- SCRAM users ---
export interface SCRAMCredential {
  mechanism: string;
  iterations: number;
}
export interface SCRAMUser {
  user: string;
  credentials: SCRAMCredential[];
}
export async function listSCRAMUsers(cluster: string): Promise<SCRAMUser[]> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/users`));
  if (!r.ok) throw new Error(await r.text());
  const data = (await r.json()) as { users?: SCRAMUser[] };
  return data.users ?? [];
}
export async function upsertSCRAMUser(
  cluster: string,
  req: { user: string; mechanism: string; password: string; iterations?: number },
): Promise<void> {
  const r = await fetchAPI(cluster, clusterPath(cluster, `/users`), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(req),
  });
  if (!r.ok) {
    const t = await r.text();
    try {
      throw new Error((JSON.parse(t) as { error?: string }).error ?? t);
    } catch {
      throw new Error(t);
    }
  }
}
export async function deleteSCRAMUser(cluster: string, user: string, mechanism?: string): Promise<void> {
  const qs = mechanism ? `?mechanism=${encodeURIComponent(mechanism)}` : "";
  const r = await fetchAPI(cluster, clusterPath(cluster, `/users/${encodeURIComponent(user)}${qs}`),
    { method: "DELETE" },
  );
  if (!r.ok) {
    const t = await r.text();
    try {
      throw new Error((JSON.parse(t) as { error?: string }).error ?? t);
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
      const b = (await res.json()) as { error?: string };
      detail = b.error ? `: ${b.error}` : "";
    } catch {
      /* ignore */
    }
    throw new Error(`HTTP ${res.status}${detail}`);
  }
  return (await res.json()) as ClusterInfo;
}
