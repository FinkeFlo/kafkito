import { clsx } from "clsx";
import { X } from "lucide-react";

/**
 * Canonical status indicator. Pairs colour with a non-colour cue (filled
 * disc for healthy, hollow ring for warning, small ring for unknown, bold
 * X for danger)
 * and an `aria-label` so meaning survives monochrome / high-contrast /
 * screen-reader contexts (WCAG 1.4.1).
 */
export type StatusIntent = "success" | "warning" | "danger" | "neutral";

const fillByIntent: Record<StatusIntent, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
  neutral: "bg-subtle-text",
};

const textByIntent: Record<StatusIntent, string> = {
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
  neutral: "text-subtle-text",
};

const ringByIntent: Record<StatusIntent, string> = {
  success: "border-success",
  warning: "border-warning",
  danger: "border-danger",
  neutral: "border-subtle-text",
};

function intentLabel(intent: StatusIntent): string {
  switch (intent) {
    case "success":
      return "healthy";
    case "warning":
      return "degraded";
    case "danger":
      return "unhealthy";
    default:
      return "unknown";
  }
}

export interface StatusDotProps {
  /** Convenience boolean — true → success, false → danger. Use `intent` for tri-state. */
  reachable?: boolean;
  /** Explicit intent. Wins over `reachable` when both are passed. */
  intent?: StatusIntent;
  pulsing?: boolean;
  className?: string;
  /**
   * Optional override label. If omitted, derived from intent
   * ("healthy" / "degraded" / "unhealthy" / "unknown").
   */
  label?: string;
  /** Hides the SR-only label (caller has provided their own labelled wrapper). */
  hideLabel?: boolean;
  /** Forwarded to the wrapper for legacy mouse-only tooltips. */
  title?: string;
}

export function StatusDot({
  reachable,
  intent,
  pulsing = false,
  className,
  label,
  hideLabel,
  title,
}: StatusDotProps) {
  const resolved: StatusIntent =
    intent ?? (reachable === undefined ? "neutral" : reachable ? "success" : "danger");
  const text = label ?? intentLabel(resolved);

  // Non-colour cue: success is a filled disc, warning is a hollow ring,
  // danger is a bold cross (no disc at all), neutral is a smaller hollow
  // ring. Healthy vs. unhealthy must never differ by red/green alone.
  const dot =
    resolved === "warning" ? (
      <span
        aria-hidden="true"
        className={clsx("inline-block h-2 w-2 rounded-full border", ringByIntent[resolved])}
      />
    ) : resolved === "danger" ? (
      <X
        aria-hidden="true"
        strokeWidth={4}
        className={clsx("h-2.5 w-2.5 shrink-0", textByIntent[resolved])}
      />
    ) : resolved === "neutral" ? (
      <span
        aria-hidden="true"
        className={clsx("inline-block h-1.5 w-1.5 rounded-full border", ringByIntent[resolved])}
      />
    ) : (
      <span
        aria-hidden="true"
        className={clsx("inline-block h-2 w-2 rounded-full", fillByIntent[resolved])}
      />
    );

  const sr = hideLabel ? null : <span className="sr-only">{text}</span>;
  // A bare role="img" without a name is an axe violation, so a hidden label
  // hides the whole indicator instead.
  const a11y = hideLabel
    ? ({ "aria-hidden": true } as const)
    : ({ role: "img", "aria-label": text } as const);

  if (!pulsing || resolved !== "success") {
    return (
      <span
        title={title}
        {...a11y}
        className={clsx("inline-flex h-2 w-2 items-center justify-center", className)}
      >
        {dot}
        {sr}
      </span>
    );
  }
  return (
    <span title={title} {...a11y} className={clsx("relative inline-flex h-2 w-2", className)}>
      <span
        aria-hidden="true"
        className={clsx(
          "absolute inset-0 animate-ping rounded-full opacity-60",
          fillByIntent[resolved],
        )}
      />
      {dot}
      {sr}
    </span>
  );
}
