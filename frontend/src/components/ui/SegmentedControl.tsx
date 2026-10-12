import { useId, type ReactNode } from "react";
import { clsx } from "clsx";

export interface SegmentedControlOption<T extends string> {
  value: T;
  content: ReactNode;
  title?: string;
}

/**
 * A small single-choice switch ("Exact | Semantic", the theme picker).
 *
 * Native radios (visually hidden) instead of `role="radio"` buttons: the
 * browser provides the arrow-key roving focus a radio group needs, and the
 * focus ring moves to the visible label via `has-[:focus-visible]`.
 */
export function SegmentedControl<T extends string>({
  label,
  value,
  onChange,
  options,
  className,
  itemClassName,
}: {
  /** Accessible name of the group. */
  label: string;
  value: T;
  onChange: (value: T) => void;
  options: SegmentedControlOption<T>[];
  className?: string;
  itemClassName?: string;
}) {
  const name = useId();
  return (
    <fieldset
      aria-label={label}
      className={clsx("inline-flex rounded-md border border-border bg-bg p-0.5", className)}
    >
      {options.map((opt) => {
        const active = value === opt.value;
        return (
          <label
            key={opt.value}
            title={opt.title}
            className={clsx(
              "inline-flex h-6 cursor-pointer items-center justify-center rounded transition-colors",
              "has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-focus",
              active ? "bg-panel text-text" : "text-muted hover:text-text",
              itemClassName,
            )}
          >
            <input
              type="radio"
              name={name}
              value={opt.value}
              checked={active}
              onChange={() => onChange(opt.value)}
              className="sr-only"
            />
            {opt.content}
          </label>
        );
      })}
    </fieldset>
  );
}
