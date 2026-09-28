import { useCallback, useEffect, useState } from "react";
import type { Message } from "@/lib/api";

const SEEN_KEY = "kafkito.coachmark.livejson.seen";

/**
 * One-time tip pointing at click-to-filter. Shown until dismissed, scrolled
 * past or 8 s after a JSON row appears; the dismissal is remembered.
 */
export function useJsonCoachmark(messages: Message[]) {
  const [showCoachmark, setShowCoachmark] = useState(() => {
    try {
      return localStorage.getItem(SEEN_KEY) !== "1";
    } catch {
      return false;
    }
  });

  const dismiss = useCallback(() => {
    setShowCoachmark(false);
    try {
      localStorage.setItem(SEEN_KEY, "1");
    } catch {
      // ignore quota / privacy-mode failures
    }
  }, []);

  // Points the coachmark at a row that actually renders the click-to-filter
  // tree. Since the search fix the backend keeps reporting "json" for values
  // it truncated mid-structure, and those rows show a "Load full value"
  // button instead of a clickable tree — teaching on one would be misleading.
  const firstJsonIdx = messages.findIndex((m) => m.value_encoding === "json" && !m.value_truncated);

  useEffect(() => {
    if (!showCoachmark) return;
    if (firstJsonIdx < 0) return; // don't burn the timer if there's no JSON to teach about
    const timer = setTimeout(dismiss, 8000);
    const onScroll = () => dismiss();
    window.addEventListener("scroll", onScroll, { once: true });
    return () => {
      clearTimeout(timer);
      window.removeEventListener("scroll", onScroll);
    };
  }, [showCoachmark, firstJsonIdx, dismiss]);

  return { visible: showCoachmark && firstJsonIdx >= 0, dismiss };
}
