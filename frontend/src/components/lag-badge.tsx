import { Badge, type BadgeVariant } from "./badge";
import { formatNumber, lagVariant } from "@/lib/format";

export interface LagBadgeProps {
  /** Lag value (sum or per-partition). null/undefined → "—" rendered as neutral. */
  value: number | bigint | null | undefined;
  className?: string;
  /** When true and value is 0, render "0" as neutral instead of nothing. Default true. */
  showZero?: boolean;
}

// Non-colour cue per WCAG 1.4.1 — glyph reinforces the colour intent so
// the lag bucket is distinguishable in monochrome / colour-blind contexts.
// `▲` for "rising" buckets (warning + danger), `·` neutral, `—` unknown.
const glyphByVariant: Record<BadgeVariant, string> = {
  success: "▼",
  warning: "▲",
  danger: "▲",
  neutral: "·",
  info: "·",
};

const labelByVariant: Record<BadgeVariant, string> = {
  success: "low lag",
  warning: "elevated lag",
  danger: "critical lag",
  neutral: "normal lag",
  info: "informational",
};

/**
 * Single source of truth for consumer-lag visualization.
 * Threshold logic (§3) lives here — never compare lag values directly in feature code.
 */
export function LagBadge({ value, className, showZero = true }: LagBadgeProps) {
  if (value === null || value === undefined) {
    return (
      <Badge variant="neutral" className={className}>
        <span className="sr-only">unknown lag</span>
        <span aria-hidden="true">—</span>
      </Badge>
    );
  }
  const n = typeof value === "bigint" ? Number(value) : value;
  if (!showZero && n === 0) return null;

  const variant: BadgeVariant = lagVariant(n);
  return (
    <Badge variant={variant} className={className}>
      <span className="sr-only">{labelByVariant[variant]}: </span>
      <span aria-hidden="true" className="font-mono text-[10px] leading-none">
        {glyphByVariant[variant]}
      </span>
      <span>{formatNumber(n)}</span>
    </Badge>
  );
}
