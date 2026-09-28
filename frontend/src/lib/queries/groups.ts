import { queryOptions } from "@tanstack/react-query";
import { type ResetOffsetsRequest, fetchGroupDetail, fetchGroups, resetGroupOffsets } from "../api";
import { clusterKey } from "./cluster-key";

export const groupQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["groups", clusterKey(cluster)] as const,
      queryFn: () => fetchGroups(cluster),
    }),
  detail: (cluster: string, group: string) =>
    queryOptions({
      queryKey: ["group", clusterKey(cluster), group] as const,
      queryFn: () => fetchGroupDetail(cluster, group),
    }),
  /**
   * Dry run of an offset reset. `confirmProd` is not part of the key: it only
   * adds the confirmation header, the result is the same.
   */
  resetPreview: (
    cluster: string,
    group: string,
    body: Omit<ResetOffsetsRequest, "dry_run">,
    confirmProd: boolean,
  ) =>
    queryOptions({
      queryKey: ["reset-offsets-preview", clusterKey(cluster), group, body] as const,
      queryFn: () => resetGroupOffsets(cluster, group, { ...body, dry_run: true }, confirmProd),
    }),
};
