import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/Button";

type RangeMode = "off" | "preset" | "custom";

const PRESETS: ReadonlyArray<{ key: string; label: string }> = [
  { key: "5m", label: "Last 5 minutes" },
  { key: "15m", label: "Last 15 minutes" },
  { key: "1h", label: "Last 1 hour" },
  { key: "6h", label: "Last 6 hours" },
  { key: "24h", label: "Last 24 hours" },
  { key: "7d", label: "Last 7 days" },
  { key: "30d", label: "Last 30 days" },
  { key: "today", label: "Today" },
  { key: "yesterday", label: "Yesterday" },
];

function rangeLabel(mode: RangeMode, preset: string, from: string, to: string): string {
  if (mode === "off") return "Any time";
  if (mode === "preset") {
    return PRESETS.find((p) => p.key === preset)?.label ?? `Last ${preset}`;
  }
  if (from && to) return `${from.replace("T", " ")} → ${to.replace("T", " ")}`;
  if (from) return `From ${from.replace("T", " ")}`;
  if (to) return `Until ${to.replace("T", " ")}`;
  return "Custom";
}

// Time-range picker styled after Grafana's dashboard time picker: a single
// toolbar trigger that opens a two-column popover (absolute / quick ranges).
// Quick ranges fire-and-close; absolute requires Apply because two inputs are
// rarely meaningful mid-typing. "Any time" lives at the top as the explicit
// reset action so it isn't hidden inside a mode toggle.
export function RangePicker({
  mode,
  preset,
  customFrom,
  customTo,
  onChange,
  disabled,
}: {
  mode: RangeMode;
  preset: string;
  customFrom: string;
  customTo: string;
  onChange: (mode: RangeMode, preset: string, customFrom: string, customTo: string) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [draftFrom, setDraftFrom] = useState(customFrom);
  const [draftTo, setDraftTo] = useState(customTo);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setDraftFrom(customFrom);
    setDraftTo(customTo);
  }, [customFrom, customTo]);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  const applyPreset = (key: string) => {
    onChange("preset", key, "", "");
    setOpen(false);
  };
  const applyCustom = () => {
    if (!draftFrom && !draftTo) return;
    onChange("custom", preset, draftFrom, draftTo);
    setOpen(false);
  };
  const clear = () => {
    onChange("off", preset, "", "");
    setOpen(false);
  };

  const active = mode !== "off";
  const label = rangeLabel(mode, preset, customFrom, customTo);

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        disabled={disabled}
        className={`rounded border px-2 py-1 text-xs ${
          active
            ? "border-accent bg-accent-subtle text-accent"
            : "border-[var(--color-border)] hover:border-[var(--color-border-strong)]"
        } disabled:cursor-not-allowed disabled:opacity-50`}
        title="Filter messages by timestamp"
        aria-haspopup="dialog"
        aria-expanded={open}
      >
        {label}
        <span className="ml-1 text-[var(--color-text-muted)]">▾</span>
      </button>
      {open && (
        <div
          role="dialog"
          aria-label="Time range picker"
          className="absolute left-0 top-full z-30 mt-1 grid w-[480px] grid-cols-2 gap-4 rounded-md border border-[var(--color-border)] bg-[var(--color-panel)] p-3 shadow-lg"
        >
          <div className="flex flex-col gap-2">
            <div className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-text-muted)]">
              Absolute time range
            </div>
            <label className="flex flex-col gap-1 text-xs text-[var(--color-text-muted)]">
              From
              <input
                type="datetime-local"
                value={draftFrom}
                onChange={(e) => setDraftFrom(e.target.value)}
                className="rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1 text-[var(--color-text)]"
              />
            </label>
            <label className="flex flex-col gap-1 text-xs text-[var(--color-text-muted)]">
              To
              <input
                type="datetime-local"
                value={draftTo}
                onChange={(e) => setDraftTo(e.target.value)}
                className="rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1 text-[var(--color-text)]"
              />
            </label>
            <Button
              variant="primary"
              size="sm"
              className="mt-1"
              onClick={applyCustom}
              disabled={!draftFrom && !draftTo}
            >
              Apply
            </Button>
            {active && (
              <button
                type="button"
                onClick={clear}
                className="rounded border border-[var(--color-border)] px-2 py-1 text-xs text-[var(--color-text-muted)] hover:border-[var(--color-border-strong)]"
              >
                Any time (clear)
              </button>
            )}
          </div>
          <div className="flex flex-col gap-0.5">
            <div className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-text-muted)]">
              Quick ranges
            </div>
            {PRESETS.map((p) => (
              <button
                key={p.key}
                type="button"
                onClick={() => applyPreset(p.key)}
                className={`rounded px-2 py-1 text-left text-xs ${
                  mode === "preset" && preset === p.key
                    ? "bg-accent-subtle text-accent"
                    : "text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]"
                }`}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
