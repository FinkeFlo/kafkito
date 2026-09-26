import { queryOptions } from "@tanstack/react-query";
import { listSCRAMUsers } from "../api";

export const scramUserQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["scram-users", cluster] as const,
      queryFn: () => listSCRAMUsers(cluster),
    }),
};
