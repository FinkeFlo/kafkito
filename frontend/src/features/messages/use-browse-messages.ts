import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { PartitionInfo } from "@/lib/api";
import { messageQueries } from "@/lib/queries/messages";
import type { BrowseFrom } from "./browse-params";
import type { TimeRange } from "./time-range";

type BrowseOptions = {
  cluster: string;
  topic: string;
  partitions: PartitionInfo[];
  partition: number;
  limit: number;
  from: BrowseFrom;
  msgOffset: number;
  range: TimeRange;
  live: boolean;
  /** Disables the head-page query while a search result is on screen. */
  paused: boolean;
};

/** Head page of the message browser for the current browse filter. */
export function useBrowseMessages({
  cluster,
  topic,
  partitions,
  partition,
  limit,
  from,
  msgOffset,
  range,
  live,
  paused,
}: BrowseOptions) {
  const params = useMemo(
    () => ({
      partition,
      limit,
      from,
      // Single partition selected: seek that partition. Partition = all:
      // seek every partition to the same offset via partition_offsets so
      // "from offset" works across the whole topic, not just one partition.
      offset: from === "offset" && partition >= 0 ? msgOffset : undefined,
      partitionOffsets:
        from === "offset" && partition < 0 && partitions.length > 0
          ? Object.fromEntries(partitions.map((p) => [p.partition, msgOffset]))
          : undefined,
      from_ts_ms: range.from_ts_ms,
      to_ts_ms: range.to_ts_ms,
    }),
    [partition, limit, from, msgOffset, partitions, range.from_ts_ms, range.to_ts_ms],
  );

  const query = useQuery({
    ...messageQueries.page(cluster, topic, params),
    refetchInterval: live ? 2_000 : false,
    enabled: !paused,
  });

  return { params, query };
}
