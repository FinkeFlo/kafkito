import { EyeOff } from "lucide-react";

/**
 * Marks a message part the cluster's data masking rules redacted. The
 * crossed-out eye and the text carry the meaning; the tint is decoration.
 */
export function MaskedBadge({ label = "masked", title }: { label?: string; title: string }) {
  return (
    <span
      title={title}
      className="inline-flex items-center gap-1 rounded bg-[var(--color-warning-subtle)] px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-warning)]"
    >
      <EyeOff aria-hidden="true" className="h-3 w-3 shrink-0" />
      {label}
    </span>
  );
}
