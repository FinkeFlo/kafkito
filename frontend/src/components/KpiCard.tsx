import type { ReactNode } from "react";
import { clsx } from "clsx";
import { StatusIcon } from "./status-icon";

type DeltaIntent = "good" | "bad" | "neutral";

/**
 * KPI card with label / value / unit / optional delta. A good or bad delta
 * carries a check or cross icon next to its colour so the verdict survives
 * colour-blind and monochrome contexts (WCAG 1.4.1); neutral deltas stay
 * plain muted text.
 */
export function KpiCard({
  label,
  value,
  unit,
  delta,
  deltaIntent = "neutral",
  className,
}: {
  label: string;
  value: ReactNode;
  unit?: ReactNode;
  delta?: ReactNode;
  deltaIntent?: DeltaIntent;
  className?: string;
}) {
  const hasDelta = delta !== undefined && delta !== null && delta !== "";

  return (
    <div className={clsx("rounded-xl border border-border bg-panel p-4", className)}>
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted">{label}</div>
      <div className="mt-2 flex items-baseline gap-2">
        <div className="text-2xl font-semibold tabular-nums">{value}</div>
        {unit !== undefined && unit !== null && (
          <div className="text-xs text-subtle-text">{unit}</div>
        )}
      </div>
      {hasDelta && (
        <div
          className={clsx(
            "mt-1 flex items-center gap-1 text-xs font-medium",
            deltaIntent === "good" && "text-success",
            deltaIntent === "bad" && "text-danger",
            deltaIntent === "neutral" && "text-muted",
          )}
        >
          {deltaIntent === "good" && <StatusIcon intent="success" label="Good" />}
          {deltaIntent === "bad" && <StatusIcon intent="danger" label="Needs attention" />}
          {delta}
        </div>
      )}
    </div>
  );
}
