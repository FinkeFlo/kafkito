import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  /** Bumps padding from p-4 → p-5 for the overview/hero card on a page. */
  hero?: boolean;
  /** Removes the default padding. Use for tables that should reach the card edge. */
  flush?: boolean;
}

export function Card({ className, hero, flush, children, ...rest }: CardProps) {
  return (
    <div
      className={cn(
        "rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-raised)]",
        !flush && (hero ? "p-5" : "p-4"),
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
