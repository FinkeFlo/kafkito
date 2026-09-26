import { queryOptions } from "@tanstack/react-query";
import { fetchBrokers } from "../api";

export const brokerQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["brokers", cluster] as const,
      queryFn: () => fetchBrokers(cluster),
    }),
};
