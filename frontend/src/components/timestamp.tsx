import { useTimeZone } from "@/lib/use-timezone";
import { formatTimestamp, type TimestampZone } from "@/lib/format";
import { cn } from "@/lib/utils";

export interface TimestampProps {
  /** Date | epoch ms | ISO string */
  value: Date | number | string | null | undefined;
  /** Override the global timezone preference for this single instance. */
  zone?: TimestampZone;
  className?: string;
}

/**
 * ISO 8601 timestamp with millisecond precision. Reads the global UTC ↔ local
 * toggle from useTimeZone() unless `zone` is set. Always rendered in font-mono.
 */
export function Timestamp({ value, zone, className }: TimestampProps) {
  const [globalZone] = useTimeZone();
  const effective = zone ?? globalZone;
  const text = formatTimestamp(value, effective);
  const iso = formatTimestamp(value, "utc");
  return (
    <time
      dateTime={iso === "—" ? undefined : iso}
      title={iso === "—" ? undefined : iso}
      className={cn("font-mono text-xs text-[var(--color-text)]", className)}
    >
      {text}
    </time>
  );
}
