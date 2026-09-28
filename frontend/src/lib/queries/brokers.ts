import { queryOptions } from "@tanstack/react-query";
import { fetchBrokers } from "../api";
import { clusterKey } from "./cluster-key";

export const brokerQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["brokers", clusterKey(cluster)] as const,
      queryFn: () => fetchBrokers(cluster),
    }),
};
