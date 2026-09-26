import type { ReactNode } from "react";
import { clsx } from "clsx";

type TagVariant = "neutral" | "info" | "warn" | "danger" | "success";

const variants: Record<TagVariant, string> = {
  neutral: "bg-subtle text-muted border-border",
  info: "bg-accent-subtle text-accent border-transparent",
  warn: "bg-tint-amber-bg text-tint-amber-fg border-transparent",
  danger: "bg-tint-red-bg text-tint-red-fg border-transparent",
  success: "bg-tint-green-bg text-tint-green-fg border-transparent",
};

export function Tag({
  children,
  variant = "neutral",
  className,
}: {
  children: ReactNode;
  variant?: TagVariant;
  className?: string;
}) {
  return (
    <span
      className={clsx(
        "inline-flex items-center rounded-sm border px-1.5 py-0.5 font-mono text-[10px] font-medium uppercase tracking-wide",
        variants[variant],
        className,
      )}
    >
      {children}
    </span>
  );
}
