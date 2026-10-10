import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/utils";

export type BadgeVariant = "success" | "warning" | "danger" | "neutral" | "info";

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: BadgeVariant;
  leadingIcon?: ReactNode;
}

const variantMap: Record<BadgeVariant, string> = {
  success: "bg-tint-green-bg text-success ring-success/30",
  warning: "bg-tint-amber-bg text-warning ring-warning/30",
  danger: "bg-tint-red-bg text-danger ring-danger/30",
  neutral: "bg-subtle text-muted ring-border",
  info: "bg-accent-subtle text-accent ring-accent/30",
};

export function Badge({
  variant = "neutral",
  leadingIcon,
  className,
  children,
  ...rest
}: BadgeProps) {
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
