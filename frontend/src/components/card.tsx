import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  /** Reduce padding from p-6 → p-4 for compact list cards. */
  compact?: boolean;
  /** Removes the default padding. Use for tables that should reach the card edge. */
  flush?: boolean;
}

export function Card({ className, compact, flush, children, ...rest }: CardProps) {
  return (
    <div
      className={cn(
        "rounded-2xl border border-[var(--color-border)] bg-[var(--color-surface-raised)] shadow-sm",
        !flush && (compact ? "p-4" : "p-6"),
        className,
      )}
      {...rest}
    >
      {children}
    </div>
  );
}

export interface CardHeaderProps extends HTMLAttributes<HTMLDivElement> {}

export function CardHeader({ className, children, ...rest }: CardHeaderProps) {
  return (
    <div
      className={cn(
        "flex items-center justify-between gap-4 border-b border-[var(--color-border)] px-6 py-4",
        className,
      )}
      {...rest}
    >
      {children}
    </div>
  );
}

export function CardTitle({ className, children, ...rest }: HTMLAttributes<HTMLHeadingElement>) {
  return (
    <h3 className={cn("text-sm font-semibold text-[var(--color-text)]", className)} {...rest}>
      {children}
    </h3>
  );
}
