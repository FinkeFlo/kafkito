import { useId, useState } from "react";
import { Highlight } from "@/components/ui/Highlight";
import { useFuzzy } from "@/lib/fuzzy";
import { useFormatters } from "@/lib/use-formatters";
import type { HeaderKeyStat } from "./header-keys";

const TOP_N = 12;
const FUZZY_KEYS = ["key"] as const;

/**
 * Header key input of the header search mode: a combobox that suggests the
 * header keys of the loaded messages with the number of messages that carry
 * each; any key can still be typed. Focus stays in the input: ↑/↓ move
 * through the suggestions, Enter picks one, Escape closes the list.
 */
export function HeaderKeySense({
  id,
  value,
  onChange,
  stats,
  total,
}: {
  /** Id of the input, so a sibling `<label htmlFor>` can name it. */
  id?: string;
  value: string;
  onChange: (next: string) => void;
  /** Header keys of the loaded messages, most frequent first. */
  stats: HeaderKeyStat[];
  /** Number of loaded messages the counts refer to. */
  total: number;
}) {
  const fmt = useFormatters();
  const listId = useId();
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  // Same token-wise substring matching as PathSense and the topic list.
  const fuzzy = useFuzzy(stats, { keys: FUZZY_KEYS, query: value });
  const filtered = value.trim() ? fuzzy.results : stats.slice(0, TOP_N);
  const optionId = (i: number) => `${listId}-${i}`;

  const close = () => {
    setOpen(false);
    setActive(-1);
  };
  const pick = (key: string) => {
    onChange(key);
    close();
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      setActive((i) => Math.min(i + 1, filtered.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter" && open && active >= 0 && filtered[active]) {
      e.preventDefault();
      pick(filtered[active].key);
    } else if (e.key === "Escape") {
      close();
    }
  };

  return (
    <div className="relative">
      <input
        id={id}
        role="combobox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-autocomplete="list"
        aria-activedescendant={open && active >= 0 ? optionId(active) : undefined}
        autoComplete="off"
        maxLength={1024}
        value={value}
        placeholder="Type or ↓ for header keys"
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
          setActive(-1);
        }}
        onFocus={() => setOpen(true)}
        onBlur={close}
        onKeyDown={onKeyDown}
        className="w-full rounded border border-border bg-panel px-2 py-1 font-mono text-xs"
      />
      {open && (
        <div className="absolute z-10 mt-1 w-full min-w-72 rounded-md border border-border bg-panel shadow-xl">
          {filtered.length === 0 ? (
            <div id={listId} className="p-2 text-xs text-muted">
              {stats.length === 0
                ? "No headers in the loaded messages. Type the key."
                : "No matches"}
            </div>
          ) : (
            <div
              id={listId}
              role="listbox"
              aria-label="Header keys"
              className="max-h-72 overflow-auto py-1 text-xs"
            >
              {filtered.map((s, i) => (
                // The input keeps focus and handles the keyboard through
                // aria-activedescendant, so the options stay out of the tab
                // order and take only the pointer.
                <button
                  key={s.key}
                  id={optionId(i)}
                  type="button"
                  role="option"
                  tabIndex={-1}
                  aria-selected={i === active}
                  // Keeps focus in the input, so the pick lands before blur
                  // closes the list.
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => pick(s.key)}
                  className={`flex w-full items-center gap-3 px-2 py-1 text-left hover:bg-accent-subtle ${i === active ? "bg-accent-subtle" : ""}`}
                >
                  <span className="min-w-0 flex-1 truncate font-mono" title={s.key}>
                    <Highlight text={s.key} ranges={fuzzy.rangesFor(s, "key")} />
                  </span>
                  <span className="shrink-0 tabular-nums text-muted">
                    {fmt.number(s.count)} of {fmt.number(total)} messages
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
