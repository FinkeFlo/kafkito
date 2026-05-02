import type { ReactNode } from "react";
import { clsx } from "clsx";

type DeltaIntent = "good" | "bad" | "neutral";

const deltaPrefix: Record<DeltaIntent, string> = {
  good: "+",
  bad: "−",
  neutral: "±",
};

const deltaSrLabel: Record<DeltaIntent, string> = {
  good: "increase",
  bad: "decrease",
  neutral: "change",
};

function isReactNodeWithExplicitSign(delta: ReactNode): boolean {
  if (typeof delta !== "string") return false;
  const trimmed = delta.trim();
  if (trimmed.length === 0) return false;
  const first = trimmed.charAt(0);
  return first === "+" || first === "-" || first === "−" || first === "±";
}

/**
 * KPI card with label / value / unit / optional delta. The delta intent is
 * conveyed by both colour AND a leading sign glyph (`+` / `−` / `±`) so
 * the meaning survives monochrome / colour-blind contexts (WCAG 1.4.1).
 * If the caller already encoded the sign (e.g. `delta="+12%"`), the
 * component does not add a second prefix.
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
  const needsPrefix = hasDelta && !isReactNodeWithExplicitSign(delta);

  return (
    <div
      className={clsx(
        "rounded-xl border border-border bg-panel p-4",
        className,
      )}
    >
      <div className="text-[11px] font-semibold uppercase tracking-wider text-muted">
        {label}
      </div>
      <div className="mt-2 flex items-baseline gap-2">
        <div className="text-2xl font-semibold tabular-nums">{value}</div>
        {unit !== undefined && unit !== null && (
          <div className="text-xs text-subtle-text">{unit}</div>
        )}
      </div>
      {hasDelta && (
        <div
          className={clsx(
            "mt-1 text-xs font-medium",
            deltaIntent === "good" && "text-success",
            deltaIntent === "bad" && "text-danger",
            deltaIntent === "neutral" && "text-muted",
          )}
        >
          <span className="sr-only">{deltaSrLabel[deltaIntent]}: </span>
          {needsPrefix ? (
            <span aria-hidden="true" className="mr-0.5 font-mono">
              {deltaPrefix[deltaIntent]}
            </span>
          ) : null}
          {delta}
        </div>
      )}
    </div>
  );
}
