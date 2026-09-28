import { useEffect, useState } from "react";

/**
 * Local draft for a numeric input so typing does not refetch on every
 * keystroke. `commit` normalizes the draft, writes it back, and applies it
 * when it differs from the current value; the draft follows outside changes.
 */
export function useNumberDraft(
  value: number,
  normalize: (raw: string) => number,
  apply: (next: number) => void,
) {
  const [draft, setDraft] = useState<string>(String(value));
  useEffect(() => {
    setDraft(String(value));
  }, [value]);

  const commit = () => {
    const next = normalize(draft);
    setDraft(String(next));
    if (next !== value) apply(next);
  };

  return { draft, setDraft, commit };
}
