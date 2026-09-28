import { queryOptions } from "@tanstack/react-query";
import { fetchTopicConsumers, fetchTopicDetail, fetchTopics } from "../api";
import { clusterKey } from "./cluster-key";

export const topicQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["topics", clusterKey(cluster)] as const,
      queryFn: () => fetchTopics(cluster),
    }),
  detail: (cluster: string, topic: string) =>
    queryOptions({
      queryKey: ["topic", clusterKey(cluster), topic] as const,
      queryFn: () => fetchTopicDetail(cluster, topic),
    }),
  consumers: (cluster: string, topic: string) =>
    queryOptions({
      queryKey: ["topic-consumers", clusterKey(cluster), topic] as const,
      queryFn: () => fetchTopicConsumers(cluster, topic),
    }),
};
