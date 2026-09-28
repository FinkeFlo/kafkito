import { useId, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { StatusIcon } from "./StatusIcon";

/**
 * Visible validation message for one form field. The ⊗ icon (named "Error")
 * and the text carry the state, so it never depends on the red tint alone.
 * Render it next to the control, outside any wrapping `<label>`, so the
 * message does not become part of the field's accessible name.
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
    <p
      id={id}
      role="alert"
      className={cn("mt-1 flex items-start gap-1 text-xs text-danger", className)}
    >
      <StatusIcon intent="danger" className="mt-px" />
      <span className="min-w-0">{children}</span>
    </p>
  );
}

/**
 * Wires a field's validation error to its control: `aria-invalid="true"`
 * plus an `aria-describedby` that points at the rendered `<FieldError>`.
 * Spread `controlProps` onto the control and render `message` below it.
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
    message: invalid ? <FieldError id={errorId}>{error}</FieldError> : null,
  };
}
