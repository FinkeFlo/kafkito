import type { ReactNode } from "react";
import { AlertTriangle, CheckCircle2, Info, XCircle, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * Shape-coded status icons. Success, warning, danger and info each use a
 * different glyph so the outcome survives colour-blind and monochrome
 * rendering (WCAG 1.4.1) — never distinguish two outcomes by tint alone.
 */
type OutcomeIntent = "success" | "warning" | "danger" | "info";

const outcomeIcons: Record<OutcomeIntent, LucideIcon> = {
  success: CheckCircle2,
  warning: AlertTriangle,
  danger: XCircle,
  info: Info,
};

const outcomeLabels: Record<OutcomeIntent, string> = {
  success: "Success",
  warning: "Warning",
  danger: "Error",
  info: "Info",
};

export function StatusIcon({
  intent,
  label,
  className,
}: {
  intent: OutcomeIntent;
  /** Accessible name; defaults to "Success" / "Warning" / "Error" / "Info". */
  label?: string;
  className?: string;
}) {
  const Icon = outcomeIcons[intent];
  return (
    <Icon
      role="img"
      aria-label={label ?? outcomeLabels[intent]}
      className={cn("h-3.5 w-3.5 shrink-0", className)}
    />
  );
}

const boxByIntent: Record<OutcomeIntent, string> = {
  success: "border-success/30 bg-tint-green-bg text-success",
  warning: "border-warning/30 bg-tint-amber-bg text-warning",
  danger: "border-danger/30 bg-tint-red-bg text-danger",
  info: "border-accent/30 bg-accent-subtle text-accent",
};

/**
 * Compact inline result box (form submit outcomes, load errors). The tint
 * is decoration; the leading icon carries the outcome. Use `Notice` for
 * larger explanatory callouts.
 */
export function StatusBox({
  intent,
  children,
  className,
}: {
  intent: OutcomeIntent;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      role={intent === "danger" || intent === "warning" ? "alert" : "status"}
      className={cn(
        "flex items-start gap-1.5 rounded-md border p-2 text-xs",
        boxByIntent[intent],
        className,
      )}
    >
      <StatusIcon intent={intent} className="mt-px" />
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
