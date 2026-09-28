import { queryOptions } from "@tanstack/react-query";
import {
  type ConsumeParams,
  type SampleResponse,
  fetchMessageCount,
  fetchMessageRawBase64,
  fetchMessages,
  fetchMessageTimeline,
  fetchSample,
} from "../api";
import { type HydratableEncoding, hydrateTruncatedSampleMessages } from "../hydrate-sample";
import { clusterKey } from "./cluster-key";

/** Key prefixes for invalidating every cached page of a topic. */
export const messageKeys = {
  topic: (cluster: string, topic: string) => ["messages", clusterKey(cluster), topic] as const,
};

type CountParams = Pick<ConsumeParams, "partition" | "from_ts_ms" | "to_ts_ms">;
type TimelineRange = { from_ts_ms: number; to_ts_ms: number; slot_ms: number };

export const messageQueries = {
  /** Head page of the message browser. */
  page: (cluster: string, topic: string, params: ConsumeParams) =>
    queryOptions({
      queryKey: [...messageKeys.topic(cluster, topic), params] as const,
      queryFn: () => fetchMessages(cluster, topic, params),
    }),
  /** Newest record, used by the produce form to offer "copy latest". */
  latestProbe: (cluster: string, topic: string) =>
    queryOptions({
      queryKey: ["produce-latest-probe", clusterKey(cluster), topic] as const,
      queryFn: () => fetchMessages(cluster, topic, { from: "end", limit: 1 }),
    }),
  count: (cluster: string, topic: string, params: CountParams) =>
    queryOptions({
      queryKey: ["message-count", clusterKey(cluster), topic, params] as const,
      queryFn: () => fetchMessageCount(cluster, topic, params),
    }),
  timeline: (cluster: string, topic: string, partition: number, range: TimelineRange) =>
    queryOptions({
      queryKey: ["message-timeline", clusterKey(cluster), topic, partition, range] as const,
      queryFn: () =>
        fetchMessageTimeline(cluster, topic, {
          partition,
          from_ts_ms: range.from_ts_ms,
          to_ts_ms: range.to_ts_ms,
          slot_ms: range.slot_ms,
        }),
    }),
  /**
   * Sample for field-path suggestions, with truncated values replaced by
   * their full raw value where the given encoding can parse them. Keyed by
   * encoding so JSONPath and XPath share an entry when they hydrate alike.
   */
  sample: (cluster: string, topic: string, encoding: HydratableEncoding) =>
    queryOptions<SampleResponse>({
      queryKey: ["sample", clusterKey(cluster), topic, encoding] as const,
      queryFn: async ({ signal }) => {
        const res = await fetchSample(cluster, topic, 5, -1);
        const messages = await hydrateTruncatedSampleMessages(
          cluster,
          topic,
          res.messages,
          signal,
          encoding,
        );
        return { ...res, messages };
      },
      staleTime: 5 * 60_000,
    }),
  /** Full raw value of one record, base64-encoded. */
  raw: (cluster: string, topic: string, partition: number, offset: number) =>
    queryOptions({
      queryKey: ["message-raw", clusterKey(cluster), topic, partition, offset] as const,
      queryFn: ({ signal }) => fetchMessageRawBase64(cluster, topic, partition, offset, signal),
      staleTime: 5 * 60_000,
      // Deliberately short: these entries are megabyte-sized base64 strings,
      // so they must not linger in the cache once nothing renders them.
      gcTime: 60_000,
      retry: false,
    }),
};
