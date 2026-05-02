import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface PageHeaderProps {
  /**
   * Optional eyebrow rendered above the title. Use the canonical class
   * string `text-[11px] font-semibold uppercase tracking-wider text-muted`
   * (already applied here) and pass mono spans for cluster names per the
   * 2026-04 identity intervention.
   */
  eyebrow?: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  /** Right-aligned primary action(s). */
  actions?: ReactNode;
  className?: string;
}

export function PageHeader({
  eyebrow,
  title,
  subtitle,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <header
      className={cn("flex items-start justify-between gap-6 pb-6", className)}
    >
      <div className="space-y-1">
        {eyebrow ? (
          <div className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            {eyebrow}
          </div>
        ) : null}
        <h1 className="text-2xl font-semibold tracking-tight text-text">
          {title}
        </h1>
        {subtitle ? (
          <p className="text-sm text-muted">{subtitle}</p>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </header>
  );
}
