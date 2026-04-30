import { clsx } from "clsx";

/**
 * Canonical status indicator. Pairs colour with a non-colour cue (filled
 * disc for healthy, hollow ring for unknown/warning, X glyph for danger)
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
  // danger draws an inner cross, neutral is a tiny hollow disc. The shape
  // change keeps the indicator distinguishable in monochrome.
  const dot =
    resolved === "warning" ? (
      <span
        aria-hidden="true"
        className={clsx(
          "inline-block h-2 w-2 rounded-full border",
          ringByIntent[resolved],
        )}
      />
    ) : resolved === "danger" ? (
      <span
        aria-hidden="true"
        className={clsx(
          "relative inline-block h-2 w-2 rounded-full",
          fillByIntent[resolved],
        )}
      >
        <span className="absolute inset-x-0 top-1/2 mx-auto block h-px w-1.5 -translate-y-1/2 bg-panel" />
      </span>
    ) : resolved === "neutral" ? (
      <span
        aria-hidden="true"
        className={clsx(
          "inline-block h-2 w-2 rounded-full border",
          ringByIntent[resolved],
        )}
      />
    ) : (
      <span
        aria-hidden="true"
        className={clsx("inline-block h-2 w-2 rounded-full", fillByIntent[resolved])}
      />
    );

  const sr = hideLabel ? null : <span className="sr-only">{text}</span>;

  if (!pulsing || resolved !== "success") {
    return (
      <span
        title={title}
        role="img"
        aria-label={hideLabel ? undefined : text}
        className={clsx("inline-flex h-2 w-2 items-center justify-center", className)}
      >
        {dot}
        {sr}
      </span>
    );
  }
  return (
    <span
      title={title}
      role="img"
      aria-label={hideLabel ? undefined : text}
      className={clsx("relative inline-flex h-2 w-2", className)}
    >
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
