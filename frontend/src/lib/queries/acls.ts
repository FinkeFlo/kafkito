import { queryOptions } from "@tanstack/react-query";
import { listACLs } from "../api";
import { clusterKey } from "./cluster-key";

export const aclQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["acls", clusterKey(cluster)] as const,
      queryFn: () => listACLs(cluster),
    }),
};
