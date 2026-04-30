import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export interface SkeletonProps extends HTMLAttributes<HTMLDivElement> {
  /** Tailwind utility for height, e.g. "h-4". Default "h-4". */
  height?: string;
  /** Tailwind utility for width, e.g. "w-32" or "w-full". Default "w-full". */
  width?: string;
  rounded?: "sm" | "md" | "lg" | "full";
}

const radius = { sm: "rounded-sm", md: "rounded-md", lg: "rounded-lg", full: "rounded-full" };

export function Skeleton({
  className,
  height = "h-4",
  width = "w-full",
  rounded = "md",
  ...rest
}: SkeletonProps) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        "animate-pulse bg-[var(--color-surface-subtle)]",
        height,
        width,
        radius[rounded],
        className,
      )}
      {...rest}
    />
  );
}

export interface SkeletonRowsProps {
  count?: number;
  className?: string;
}

export function SkeletonRows({ count = 5, className }: SkeletonRowsProps) {
  return (
    <div className={cn("space-y-3", className)}>
      {Array.from({ length: count }).map((_, i) => (
        <Skeleton key={i} height="h-10" />
      ))}
    </div>
  );
}
