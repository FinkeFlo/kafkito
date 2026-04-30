import { useEffect, useState } from "react";
import { formatRelative, formatTimestamp } from "@/lib/format";
import { cn } from "@/lib/utils";

export interface RelativeTimeProps {
  value: Date | number | string | null | undefined;
  /** Re-render every N ms to keep the value fresh. Default 60s. */
  refreshMs?: number;
  className?: string;
}

/**
 * Human-readable relative time ("3 minutes ago"). Hover shows the absolute
 * ISO 8601 timestamp via the title attribute.
 */
export function RelativeTime({ value, refreshMs = 60_000, className }: RelativeTimeProps) {
  const [, setTick] = useState(0);
  useEffect(() => {
    if (!refreshMs) return;
    const id = window.setInterval(() => setTick((t) => t + 1), refreshMs);
    return () => window.clearInterval(id);
  }, [refreshMs]);

  const text = formatRelative(value);
  const iso = formatTimestamp(value, "utc");
  return (
    <time
      dateTime={iso === "—" ? undefined : iso}
      title={iso === "—" ? undefined : iso}
      className={cn("text-xs text-[var(--color-text-muted)]", className)}
    >
      {text}
    </time>
  );
}
