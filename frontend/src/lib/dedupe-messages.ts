// dedupeMessages removes records sharing the same partition+offset, keeping the
// first occurrence. Used so the message list never renders colliding React keys
// when the search accumulator or browse/tail seam returns a record twice.
export function dedupeMessages<T extends { partition: number; offset: number }>(
  messages: T[],
): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const m of messages) {
    const key = `${m.partition}-${m.offset}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(m);
  }
  return out;
}
