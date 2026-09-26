import { queryOptions } from "@tanstack/react-query";
import { fetchClusters } from "../api";

export const clusterQueries = {
  list: () => queryOptions({ queryKey: ["clusters"] as const, queryFn: fetchClusters }),
};
