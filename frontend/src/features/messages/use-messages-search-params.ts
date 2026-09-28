import { getRouteApi } from "@tanstack/react-router";
import type { BrowseFrom } from "./browse-params";

const routeApi = getRouteApi("/clusters/$cluster/topics/$topic/messages");

/** Browse position kept in the URL: partition, page size, start point. */
export function useMessagesSearchParams() {
  const { partition, limit, from, msgOffset } = routeApi.useSearch();
  const navigate = routeApi.useNavigate();

  return {
    partition,
    limit,
    from,
    msgOffset,
    setPartition: (v: number) => navigate({ search: (prev) => ({ ...prev, partition: v }) }),
    setLimit: (v: number) => navigate({ search: (prev) => ({ ...prev, limit: v }) }),
    setFrom: (v: BrowseFrom) => navigate({ search: (prev) => ({ ...prev, from: v }) }),
    setMsgOffset: (v: number) => navigate({ search: (prev) => ({ ...prev, msgOffset: v }) }),
  };
}

export type MessagesSearchParams = ReturnType<typeof useMessagesSearchParams>;
