import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * Icon-only button. Variant naming mirrors `<Button>` (`ghost`/
 * `secondary`/`danger`). The deprecated `destructive` alias was removed
 * at the end of Phase 3 once every route callsite migrated to `danger`.
 */
export type IconButtonVariant = "ghost" | "secondary" | "danger";
export type IconButtonSize = "sm" | "md";

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Required for accessibility — describes the action. */
  "aria-label": string;
  variant?: IconButtonVariant;
  size?: IconButtonSize;
  icon: ReactNode;
}

const sizeMap: Record<IconButtonSize, string> = {
  sm: "h-8 w-8",
  md: "h-10 w-10",
};

const variantMap: Record<IconButtonVariant, string> = {
  ghost: "text-subtle-text hover:text-text hover:bg-hover",
  secondary:
    "text-text border border-border hover:border-border-hover hover:bg-hover",
  danger: "text-subtle-text hover:text-danger hover:bg-tint-red-bg",
};

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { icon, variant = "ghost", size = "md", className, type = "button", ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={cn(
        "inline-flex items-center justify-center rounded-md transition-colors duration-150",
        "disabled:opacity-50 disabled:pointer-events-none",
        sizeMap[size],
        variantMap[variant],
        className,
      )}
      {...rest}
    >
      {icon}
    </button>
  );
});
