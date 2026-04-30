import { useEffect, useRef } from "react";
import { Search, X } from "lucide-react";
import { cn } from "@/lib/utils";

export interface SearchInputProps {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  /** Inline count shown muted inside the input: `visible of total`. */
  count?: { visible: number; total: number };
  /** When true, pressing `/` anywhere focuses this input. */
  shortcut?: boolean;
  /** Extra classes on the outer wrapper. */
  className?: string;
  /** ARIA label if the input has no visible label. */
  ariaLabel?: string;
  autoFocus?: boolean;
}

/**
 * Shared filter-input primitive: Search icon on the left, input in the
 * middle, optional `X of Y` counter + `/` kbd hint on the right, and a clear
 * button when non-empty. Styled consistently with the rest of the app
 * (border-border, bg-panel, h-9 pill).
 */
export function SearchInput({
  value,
  onChange,
  placeholder,
  count,
  shortcut = true,
  className,
  ariaLabel,
  autoFocus,
}: SearchInputProps) {
  const ref = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!shortcut) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const active = document.activeElement;
      if (
        active instanceof HTMLInputElement ||
        active instanceof HTMLTextAreaElement ||
        (active instanceof HTMLElement && active.isContentEditable)
      ) {
        return;
      }
      e.preventDefault();
      ref.current?.focus();
      ref.current?.select();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [shortcut]);

  const hasValue = value.length > 0;
  const showCount = !!count && hasValue;

  return (
    <div
      className={cn(
        "flex h-9 min-w-[260px] flex-1 items-center gap-2 rounded-md border border-border bg-panel px-3",
        "focus-within:border-border-strong",
        className,
      )}
    >
      <Search className="h-3.5 w-3.5 shrink-0 text-subtle-text" aria-hidden />
      <input
        ref={ref}
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-label={ariaLabel}
        autoFocus={autoFocus}
        className="flex-1 bg-transparent text-sm text-text outline-none placeholder:text-subtle-text"
      />
      {showCount && (
        <span className="shrink-0 text-[11px] tabular-nums text-muted">
          {count!.visible} of {count!.total}
        </span>
      )}
      {hasValue ? (
        <button
          type="button"
          onClick={() => {
            onChange("");
            ref.current?.focus();
          }}
          aria-label="Clear filter"
          className="rounded p-0.5 text-subtle-text hover:bg-subtle hover:text-text"
        >
          <X className="h-3.5 w-3.5" aria-hidden />
        </button>
      ) : (
        shortcut && (
          <kbd className="shrink-0 rounded border border-border bg-subtle px-1.5 py-0.5 font-mono text-[10px] text-muted">
            /
          </kbd>
        )
      )}
    </div>
  );
}
