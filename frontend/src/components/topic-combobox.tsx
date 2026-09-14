// TopicCombobox — a styled destination-topic autocomplete used by the replay
// and bulk-copy panels. Replaces the browser-native `<input list>` /
// `<datalist>` combo, whose suggestion popup cannot be restyled and renders
// tiny, unreadable rows in most browsers.
import { useEffect, useMemo, useRef, useState } from "react";

interface TopicComboboxProps {
  value: string;
  onChange: (value: string) => void;
  /** Full topic list for the destination cluster; filtered client-side. */
  topics: string[];
  disabled?: boolean;
  placeholder?: string;
  className?: string;
}

// Caps the rendered suggestion list so a cluster with thousands of topics
// doesn't turn the dropdown into an unbounded scroll list.
const maxSuggestions = 30;

export function TopicCombobox({
  value,
  onChange,
  topics,
  disabled,
  placeholder,
  className,
}: TopicComboboxProps) {
  const [menuOpen, setMenuOpen] = useState(false);
  const fieldRef = useRef<HTMLDivElement>(null);

  const filtered = useMemo(() => {
    const q = value.trim().toLowerCase();
    const matches = q ? topics.filter((t) => t.toLowerCase().includes(q)) : topics;
    return matches.slice(0, maxSuggestions);
  }, [topics, value]);

  // Close on outside clicks — native blur fires before a suggestion's click
  // handler runs, which would otherwise discard the selection.
  useEffect(() => {
    if (!menuOpen) return;
    const handler = (e: MouseEvent) => {
      if (fieldRef.current && !fieldRef.current.contains(e.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [menuOpen]);

  return (
    <div ref={fieldRef} className="relative">
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => setMenuOpen(true)}
        placeholder={placeholder}
        disabled={disabled}
        autoComplete="off"
        className={
          className ??
          "w-full rounded-md border border-border bg-panel px-3 py-1.5 text-sm font-mono disabled:opacity-50"
        }
      />
      {menuOpen && !disabled && filtered.length > 0 && (
        <ul className="absolute z-10 mt-1 max-h-64 w-full overflow-y-auto rounded-md border border-border bg-panel py-1 text-sm shadow-lg">
          {filtered.map((t) => (
            <li key={t}>
              <button
                type="button"
                onClick={() => {
                  onChange(t);
                  setMenuOpen(false);
                }}
                className="block w-full truncate px-3 py-2 text-left font-mono hover:bg-hover"
              >
                {t}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
