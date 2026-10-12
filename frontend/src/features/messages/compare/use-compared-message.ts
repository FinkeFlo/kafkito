import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import type { Message } from "@/lib/api";
import {
  MAX_COMPARE_VALUE_BYTES,
  type MessageContent,
  messageContent,
  needsFullValue,
} from "@/lib/message-compare";
import type { MessageRef } from "@/lib/message-reference";
import { messageQueries } from "@/lib/queries/messages";

export type ComparedMessage =
  | { status: "loading" }
  | { status: "error"; error: Error; retry: () => void }
  /** No record at the offset; `next` is the next offset that holds one. */
  | { status: "missing"; next: number | null }
  | {
      status: "ready";
      message: Message;
      content: MessageContent;
      /** Why only the preview of a truncated value is compared, if it is. */
      previewReason: string | null;
    };

/**
 * Loads one side of the compare page: the record, and the full value when
 * the list returned only a preview of it.
 */
export function useComparedMessage(ref: MessageRef): ComparedMessage {
  const { cluster, topic, partition, offset } = ref;
  const single = useQuery(messageQueries.single(cluster, topic, partition, offset));
  const message = single.data?.message ?? null;
  const wantFull = message !== null && needsFullValue(message);
  const raw = useQuery({
    ...messageQueries.raw(cluster, topic, partition, offset),
    enabled: wantFull,
  });

  const previewContent = useMemo(() => (message ? messageContent(message) : null), [message]);
  const content = useMemo(() => {
    if (!message) return null;
    if (wantFull && raw.data === undefined) return null;
    return messageContent(message, wantFull ? raw.data : undefined);
  }, [message, wantFull, raw.data]);

  if (single.isError) {
    return { status: "error", error: single.error, retry: () => void single.refetch() };
  }
  if (single.isPending) return { status: "loading" };
  if (!message) return { status: "missing", next: single.data.next };

  if (wantFull && raw.isError) {
    // The comparison still runs, on the preview, and says so.
    return {
      status: "ready",
      message,
      content: previewContent ?? messageContent(message),
      previewReason: `The full value could not be loaded (${raw.error.message}).`,
    };
  }
  if (!content) return { status: "loading" };
  return { status: "ready", message, content, previewReason: previewReasonFor(message) };
}

function previewReasonFor(m: Message): string | null {
  if (m.value_truncated !== true) return null;
  if (m.masked) return "Masked values cannot be loaded in full.";
  if ((m.value_size_bytes ?? 0) > MAX_COMPARE_VALUE_BYTES) {
    return `The value is larger than ${MAX_COMPARE_VALUE_BYTES / 1024 / 1024} MB, the most the compare page loads.`;
  }
  return null;
}
