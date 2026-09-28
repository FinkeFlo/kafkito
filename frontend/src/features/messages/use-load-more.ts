import { useEffect, useRef, useState } from "react";
import { fetchMessages, type ConsumeParams, type Message, type MessagesPage } from "@/lib/api";

/**
 * Cursor pagination: the head page is fetched by the browse query; each
 * "Load more" click appends the next backward page using the previous
 * page's next_cursor. Reset whenever the head page changes so the
 * accumulated tail can never out-of-sync with the current filter.
 */
export function useLoadMore(
  cluster: string,
  topic: string,
  params: ConsumeParams,
  head: MessagesPage | undefined,
) {
  const [tailMessages, setTailMessages] = useState<Message[]>([]);
  const [tailCursor, setTailCursor] = useState<string | undefined>(undefined);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  const loadGenRef = useRef(0);

  useEffect(() => {
    loadGenRef.current += 1;
    setTailMessages([]);
    setTailCursor(head?.next_cursor);
    setLoadMoreError(null);
    setLoadingMore(false);
  }, [head]);

  const loadMore = async () => {
    if (!tailCursor) return;
    const gen = loadGenRef.current;
    setLoadingMore(true);
    setLoadMoreError(null);
    try {
      const next = await fetchMessages(cluster, topic, {
        ...params,
        cursor: tailCursor,
      });
      if (loadGenRef.current !== gen) return; // filter changed mid-flight; drop this page
      setTailMessages((prev) => [...prev, ...(next.messages ?? [])]);
      setTailCursor(next.has_more ? next.next_cursor : undefined);
    } catch (err) {
      if (loadGenRef.current !== gen) return;
      setLoadMoreError((err as Error).message);
    } finally {
      if (loadGenRef.current === gen) setLoadingMore(false);
    }
  };

  return { tailMessages, tailCursor, loadingMore, loadMoreError, loadMore };
}
