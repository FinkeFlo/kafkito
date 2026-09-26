import { queryOptions } from "@tanstack/react-query";
import { fetchTopicConsumers, fetchTopicDetail, fetchTopics } from "../api";

export const topicQueries = {
  list: (cluster: string) =>
    queryOptions({
      queryKey: ["topics", cluster] as const,
      queryFn: () => fetchTopics(cluster),
    }),
  detail: (cluster: string, topic: string) =>
    queryOptions({
      queryKey: ["topic", cluster, topic] as const,
      queryFn: () => fetchTopicDetail(cluster, topic),
    }),
  consumers: (cluster: string, topic: string) =>
    queryOptions({
      queryKey: ["topic-consumers", cluster, topic] as const,
      queryFn: () => fetchTopicConsumers(cluster, topic),
    }),
};
