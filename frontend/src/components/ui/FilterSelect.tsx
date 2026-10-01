import { useId } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

export interface FilterSelectOption<V extends string> {
  value: V;
  label: string;
}

/**
 * Toolbar filter dropdown: "Label: Value ▾" on a native `<select>`, so it
 * keeps the platform keyboard and screen-reader behaviour. The border turns
 * strong while a value other than `defaultValue` is selected. Passing
 * `disabledReason` disables the control and exposes the reason through
 * `aria-describedby` (§ 6.4).
 */
export function FilterSelect<V extends string>({
  label,
  value,
  options,
  onChange,
  defaultValue,
  disabledReason,
  className,
}: {
  label: string;
  value: V;
  options: readonly FilterSelectOption<V>[];
  onChange: (value: V) => void;
  /** The "no filter" value; defaults to the first option. */
  defaultValue?: V;
  disabledReason?: string;
  className?: string;
}) {
  const id = useId();
  const reasonId = `${id}-reason`;
  const disabled = !!disabledReason;
  const active = value !== (defaultValue ?? options[0]?.value);
  return (
    <div
      className={cn(
        "relative inline-flex h-9 items-center rounded-md border bg-panel text-xs text-muted transition-colors",
        active ? "border-border-strong" : "border-border hover:border-border-hover",
        disabled && "opacity-50",
        className,
      )}
    >
      <label htmlFor={id} className="whitespace-nowrap pl-3">
        {label}:
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value as V)}
        disabled={disabled}
        aria-describedby={disabled ? reasonId : undefined}
        className="h-full cursor-pointer appearance-none bg-transparent pr-7 pl-1 font-medium text-text disabled:cursor-not-allowed"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2 h-3.5 w-3.5" aria-hidden />
      {disabled ? (
        <span id={reasonId} className="sr-only">
          {disabledReason}
        </span>
      ) : null}
    </div>
  );
}
