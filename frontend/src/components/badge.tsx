import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/utils";

export type BadgeVariant = "success" | "warning" | "danger" | "neutral" | "info";

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: BadgeVariant;
  leadingIcon?: ReactNode;
}

const variantMap: Record<BadgeVariant, string> = {
  success:
    "bg-[var(--color-success-subtle)] text-[var(--color-success)] ring-[var(--color-success)]/30",
  warning:
    "bg-[var(--color-warning-subtle)] text-[var(--color-warning)] ring-[var(--color-warning)]/30",
  danger:
    "bg-[var(--color-danger-subtle)] text-[var(--color-danger)] ring-[var(--color-danger)]/30",
  neutral:
    "bg-[var(--color-surface-subtle)] text-[var(--color-text-muted)] ring-[var(--color-border)]",
  info:
    "bg-[var(--color-info-subtle)] text-[var(--color-info)] ring-[var(--color-info)]/30",
};

export function Badge({ variant = "neutral", leadingIcon, className, children, ...rest }: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset",
        variantMap[variant],
        className,
      )}
      {...rest}
    >
      {leadingIcon ? <span className="shrink-0">{leadingIcon}</span> : null}
      {children}
    </span>
  );
}
