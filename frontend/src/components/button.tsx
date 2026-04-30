import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * Canonical variant union per `DESIGN_GUIDELINES.md` § 6.2:
 * `primary | secondary | danger | ghost`. The deprecated `destructive`
 * alias was removed at the end of Phase 3 once every route callsite
 * migrated to `danger`.
 */
export type ButtonVariant =
  | "primary"
  | "secondary"
  | "danger"
  | "ghost";

export type ButtonSize = "sm" | "md";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  leadingIcon?: ReactNode;
  trailingIcon?: ReactNode;
  loading?: boolean;
}

const base =
  "inline-flex items-center justify-center gap-2 rounded-md font-medium " +
  "transition-colors duration-150 select-none whitespace-nowrap " +
  "disabled:opacity-50 disabled:pointer-events-none";

const variants: Record<ButtonVariant, string> = {
  primary: cn(
    "bg-accent text-accent-foreground hover:bg-accent-hover",
    "focus-visible:[outline-color:var(--color-focus-on-accent)]",
  ),
  secondary: cn(
    "border border-border bg-subtle text-text",
    "hover:bg-hover hover:border-border-hover",
  ),
  danger: cn(
    "bg-danger text-accent-foreground hover:bg-danger/90",
    "focus-visible:[outline-color:var(--color-focus-on-accent)]",
  ),
  ghost: "text-muted hover:text-text hover:bg-hover",
};

const sizes: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-xs",
  md: "h-9 px-3 py-1.5 text-sm",
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  {
    className,
    variant = "primary",
    size = "md",
    leadingIcon,
    trailingIcon,
    loading,
    children,
    disabled,
    type = "button",
    ...rest
  },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      disabled={disabled || loading}
      className={cn(base, variants[variant], sizes[size], className)}
      {...rest}
    >
      {leadingIcon ? <span className="shrink-0">{leadingIcon}</span> : null}
      <span>{children}</span>
      {trailingIcon ? <span className="shrink-0">{trailingIcon}</span> : null}
    </button>
  );
});
