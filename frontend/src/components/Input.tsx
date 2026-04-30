import { forwardRef, type InputHTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** Marks the input as having a validation error — switches the border to `border-danger`. */
  invalid?: boolean;
  /** Optional icon rendered absolute-positioned at the leading edge. */
  leadingIcon?: ReactNode;
  /** Optional icon (or button) rendered absolute-positioned at the trailing edge. */
  trailingIcon?: ReactNode;
  /** Wrapper class — placed on the relative container when icons are passed. */
  wrapperClassName?: string;
}

/**
 * Canonical text input. Matches toolbar height (`h-9`), uses `bg-panel` +
 * `border-border` (with `border-border-hover` on hover for affordance,
 * `border-danger` for validation errors) and intentionally does NOT set
 * `outline-none` — the global `:focus-visible` rule provides the visible
 * focus indicator.
 */
export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  {
    className,
    wrapperClassName,
    invalid,
    leadingIcon,
    trailingIcon,
    type = "text",
    ...rest
  },
  ref,
) {
  const base = cn(
    "h-9 w-full rounded-md border bg-panel px-3 text-sm text-text",
    "placeholder:text-subtle-text",
    "transition-colors duration-150",
    invalid ? "border-danger" : "border-border hover:border-border-hover",
    "disabled:cursor-not-allowed disabled:opacity-60",
    leadingIcon && "pl-9",
    trailingIcon && "pr-9",
    className,
  );

  if (!leadingIcon && !trailingIcon) {
    return <input ref={ref} type={type} className={base} {...rest} />;
  }

  return (
    <div className={cn("relative inline-flex w-full items-center", wrapperClassName)}>
      {leadingIcon ? (
        <span
          aria-hidden="true"
          className="pointer-events-none absolute left-3 flex h-4 w-4 items-center justify-center text-subtle-text"
        >
          {leadingIcon}
        </span>
      ) : null}
      <input ref={ref} type={type} className={base} {...rest} />
      {trailingIcon ? (
        <span className="absolute right-2 flex h-5 w-5 items-center justify-center text-subtle-text">
          {trailingIcon}
        </span>
      ) : null}
    </div>
  );
});
