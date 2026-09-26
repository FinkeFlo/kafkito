// useMessageRawValue — shared TanStack Query hook for the raw-download
// endpoint (`…/messages/{partition}/{offset}/raw`).
//
// Two places need the untruncated value of a message: ReplayModal (to
// re-produce a record whose list representation was capped at 64 KB) and
// ValueBody (to enable click-to-filter on a large JSON value). Both used to
// hand-roll their own fetch + cancellation state machine. Going through
// useQuery instead gives all of them caching (collapsing and re-expanding a
// row no longer refetches megabytes), automatic abort on unmount, and the
// shared loading/error state shape the rest of the app uses.
import { useQuery } from "@tanstack/react-query";
import { messageQueries } from "@/lib/queries/messages";

export function useMessageRawValue(params: {
  cluster: string;
  topic: string;
  partition: number;
  offset: number;
  enabled: boolean;
}) {
  const { cluster, topic, partition, offset, enabled } = params;
  return useQuery({
    ...messageQueries.raw(cluster, topic, partition, offset),
    enabled: enabled && !!cluster && !!topic,
  });
}
