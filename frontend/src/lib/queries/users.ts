import { queryOptions } from "@tanstack/react-query";
import { listSCRAMUsers } from "../api";
import { clusterKey } from "./cluster-key";

export const scramUserQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["scram-users", clusterKey(cluster)] as const,
      queryFn: () => listSCRAMUsers(cluster),
    }),
};
