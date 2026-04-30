import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface PillProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  active?: boolean;
  leadingIcon?: ReactNode;
}

/**
 * Filter chip / segmented control item. Used for cluster switcher items,
 * tag filters etc.
 */
export function Pill({ active, leadingIcon, className, children, type = "button", ...rest }: PillProps) {
  return (
    <button
      type={type}
      data-active={active ? "" : undefined}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition-colors duration-150",
        active
          ? "border-[var(--color-accent)] bg-[var(--color-accent-subtle)] text-[var(--color-accent)]"
          : "border-[var(--color-border)] bg-[var(--color-surface-raised)] text-[var(--color-text-muted)] hover:border-[var(--color-border-strong)] hover:text-[var(--color-text)]",
        className,
      )}
      {...rest}
    >
      {leadingIcon ? <span className="shrink-0">{leadingIcon}</span> : null}
      {children}
    </button>
  );
}
