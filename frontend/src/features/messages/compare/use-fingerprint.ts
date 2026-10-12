import { useEffect, useState } from "react";
import type { MessageContent } from "@/lib/message-compare";
import {
  type FingerprintOptions,
  type FingerprintResult,
  messageFingerprint,
} from "@/lib/message-fingerprint";

/**
 * The fingerprint of `content` under `options`; undefined while it is being
 * computed. Hashing is local work (Web Crypto), not a request, so it does
 * not go through TanStack Query.
 */
export function useFingerprint(
  content: MessageContent | null,
  options: FingerprintOptions,
): FingerprintResult | undefined {
  const [result, setResult] = useState<FingerprintResult | undefined>(undefined);
  const { semantic, ignoreArrayOrder, ignoreChildOrder, ignoreKafkitoHeaders } = options;
  useEffect(() => {
    setResult(undefined);
    if (!content) return;
    let live = true;
    messageFingerprint(content, {
      semantic,
      ignoreArrayOrder,
      ignoreChildOrder,
      ignoreKafkitoHeaders,
    }).then(
      (r) => {
        if (live) setResult(r);
      },
      (e: unknown) => {
        if (live) setResult({ ok: false, reason: (e as Error).message });
      },
    );
    return () => {
      live = false;
    };
  }, [content, semantic, ignoreArrayOrder, ignoreChildOrder, ignoreKafkitoHeaders]);
  return result;
}
