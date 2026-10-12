// A message reference names one record: `<cluster>/<topic>/<partition>/<offset>`.
// "Copy reference" in the message list writes it, the compare page reads it.
// Kafka topic names cannot contain "/", so the string is parsed from the
// right and a cluster name may contain any character, "/" included.

export interface MessageRef {
  cluster: string;
  topic: string;
  partition: number;
  offset: number;
}

const TOPIC_RE = /^[a-zA-Z0-9._-]+$/;

export function formatMessageRef(ref: MessageRef): string {
  return `${ref.cluster}/${ref.topic}/${ref.partition}/${ref.offset}`;
}

/** The reference in `text`, or null when it is not one. */
export function parseMessageRef(text: string): MessageRef | null {
  const parts = text.trim().split("/");
  if (parts.length < 4) return null;
  const offset = parseIndex(parts.pop() as string);
  const partition = parseIndex(parts.pop() as string);
  const topic = parts.pop() as string;
  const cluster = parts.join("/");
  if (offset === null || partition === null || !cluster || !TOPIC_RE.test(topic)) return null;
  if (partition > 2_147_483_647) return null;
  return { cluster, topic, partition, offset };
}

/** A non-negative decimal integer within the safe range, else null. */
export function parseIndex(text: string): number | null {
  if (!/^\d{1,16}$/.test(text)) return null;
  const n = Number(text);
  return Number.isSafeInteger(n) ? n : null;
}
