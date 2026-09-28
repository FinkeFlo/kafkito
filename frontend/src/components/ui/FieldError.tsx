import { useId, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { StatusIcon } from "./StatusIcon";

/**
 * Visible validation message for one form field. The ⊗ icon (named "Error")
 * and the text carry the state, so it never depends on the red tint alone.
 * Render it next to the control, outside any wrapping `<label>`, so the
 * message does not become part of the field's accessible name.
 *
 * Deliberately not `role="alert"`: fields validate while the user types, and
 * an assertive announcement would interrupt every keystroke that makes the
 * value invalid. `useFieldError` puts it in a polite live region instead.
 */
function FieldError({
  id,
  children,
  className,
}: {
  id: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <p id={id} className={cn("mt-1 flex items-start gap-1 text-xs text-danger", className)}>
      <StatusIcon intent="danger" className="mt-px" />
      <span className="min-w-0">{children}</span>
    </p>
  );
}

/**
 * Wires a field's validation error to its control: `aria-invalid="true"`
 * plus an `aria-describedby` that points at the rendered `<FieldError>`.
 * Spread `controlProps` onto the control and render `message` below it,
 * unconditionally: `message` is an `aria-live="polite"` region that stays
 * mounted with the field, so an error that appears later (after typing or
 * submit) is announced once the user pauses, without interrupting them.
 * `describedBy` keeps an existing description (hint text) in the list.
 */
export function useFieldError(error: string | null | undefined, describedBy?: string) {
  const errorId = useId();
  const invalid = !!error;
  const ids = [describedBy, invalid ? errorId : undefined].filter(Boolean).join(" ");
  return {
    invalid,
    controlProps: {
      "aria-invalid": invalid ? (true as const) : undefined,
      "aria-describedby": ids || undefined,
    },
    message: (
      <div aria-live="polite">{invalid ? <FieldError id={errorId}>{error}</FieldError> : null}</div>
    ),
  };
}
