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
