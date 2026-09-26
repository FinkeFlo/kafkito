import { queryOptions } from "@tanstack/react-query";
import { listACLs } from "../api";

export const aclQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["acls", cluster] as const,
      queryFn: () => listACLs(cluster),
    }),
};
