import { useCallback, useEffect, useId, useRef, useState } from "react";
import { ChevronDown, LogOut, Monitor, Moon, Sun } from "lucide-react";
import { clsx } from "clsx";
import { useAuth } from "@/auth/hooks";
import { useTheme, type ThemePreference } from "@/lib/theme";
import { useTimeZone, type TimeZoneMode } from "@/lib/use-timezone";

const TAIL_BUFFER_KEY = "kafkito.tailBuffer";
const TAIL_BUFFER_DEFAULT = "10000";
const CONFIRM_DESTRUCTIVE_KEY = "kafkito.confirmDestructive";
type ConfirmDestructiveMode = "always" | "cluster" | "never";
const CONFIRM_DESTRUCTIVE_DEFAULT: ConfirmDestructiveMode = "always";

function readLocalStorage(key: string, fallback: string): string {
  if (typeof window === "undefined") return fallback;
  try {
    return window.localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

function writeLocalStorage(key: string, value: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(key, value);
  } catch {
    /* ignore */
  }
}

function useStoredString(key: string, fallback: string): [string, (v: string) => void] {
  const [value, setValue] = useState<string>(() => readLocalStorage(key, fallback));
  const set = useCallback(
    (v: string) => {
      setValue(v);
      writeLocalStorage(key, v);
    },
    [key],
  );
  return [value, set];
}

function initials(label: string): string {
  const parts = label.trim().split(/\s+/).slice(0, 2);
  return parts.map((p) => p.charAt(0).toUpperCase()).join("") || "?";
}

export function UserMenu() {
  const { currentUser, me, isLoading } = useAuth();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const popoverId = useId();

  const close = useCallback(() => {
    setOpen(false);
    triggerRef.current?.focus();
  }, []);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
      }
    };
    const onClick = (e: MouseEvent) => {
      const target = e.target as Node;
      if (
        popoverRef.current &&
        !popoverRef.current.contains(target) &&
        !triggerRef.current?.contains(target)
      ) {
        setOpen(false);
      }
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onClick);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onClick);
    };
  }, [open, close]);

  if (isLoading) return <span className="text-xs text-muted">…</span>;
  if (!currentUser) return null;

  const display =
    currentUser.displayName || currentUser.name || currentUser.email;

  return (
    <div className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={popoverId}
        aria-label={`Account menu for ${display}`}
        className="inline-flex h-8 items-center gap-2 rounded-full border border-border bg-panel pl-1 pr-2 text-xs text-text transition-colors hover:bg-hover"
      >
        <span
          aria-hidden
          className="flex h-6 w-6 items-center justify-center rounded-full bg-accent-subtle font-semibold text-accent"
        >
          {initials(display)}
        </span>
        <span className="hidden max-w-[12rem] truncate text-[12px] font-medium md:inline">
          {display}
        </span>
        <ChevronDown className="h-3.5 w-3.5 text-muted" aria-hidden />
      </button>

      {open && (
        <div
          ref={popoverRef}
          id={popoverId}
          role="menu"
          aria-label="Account and settings"
          tabIndex={-1}
          className="absolute right-0 z-30 mt-2 w-72 rounded-xl border border-border bg-panel p-1.5 shadow-xl"
        >
          <div className="px-2 pb-1 pt-1">
            <div className="text-[12px] font-semibold text-text">{display}</div>
            {me?.scopes?.length ? (
              <div className="mt-0.5 truncate text-[11px] text-muted">
                {me.scopes.join(", ")}
              </div>
            ) : null}
          </div>

          <div className="my-1 border-t border-border" />

          <ThemeRow />
          <TimezoneRow />
          <TailBufferRow />
          <ConfirmDestructiveRow />

          <div className="my-1 border-t border-border" />

          <a
            href="/logout"
            role="menuitem"
            className="flex items-center gap-2 rounded-md px-2.5 py-2 text-xs text-muted transition-colors hover:bg-hover hover:text-text"
          >
            <LogOut className="h-3.5 w-3.5" aria-hidden />
            <span>Log out</span>
          </a>
        </div>
      )}
    </div>
  );
}

const THEME_OPTIONS: { value: ThemePreference; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
];

const THEME_ICON: Record<ThemePreference, typeof Sun> = {
  light: Sun,
  dark: Moon,
  system: Monitor,
};

function ThemeRow() {
  const { preference, setPreference } = useTheme();
  return (
    <SettingRow
      label="Theme"
      control={
        <div role="radiogroup" aria-label="Theme" className="inline-flex rounded-md border border-border bg-bg p-0.5">
          {THEME_OPTIONS.map((opt) => {
            const Icon = THEME_ICON[opt.value];
            const active = preference === opt.value;
            return (
              <button
                key={opt.value}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => setPreference(opt.value)}
                title={opt.label}
                className={clsx(
                  "inline-flex h-6 w-7 items-center justify-center rounded transition-colors",
                  active
                    ? "bg-panel text-text"
                    : "text-muted hover:text-text",
                )}
              >
                <Icon className="h-3.5 w-3.5" aria-hidden />
                <span className="sr-only">{opt.label}</span>
              </button>
            );
          })}
        </div>
      }
    />
  );
}

const TIMEZONE_OPTIONS: { value: TimeZoneMode; label: string }[] = [
  { value: "utc", label: "UTC" },
  { value: "local", label: "Local" },
];

function TimezoneRow() {
  const [mode, setMode] = useTimeZone();
  return (
    <SettingRow
      label="Timezone"
      control={
        <div role="radiogroup" aria-label="Timezone" className="inline-flex rounded-md border border-border bg-bg p-0.5 text-[11px] font-semibold uppercase tracking-wide">
          {TIMEZONE_OPTIONS.map((opt) => {
            const active = mode === opt.value;
            return (
              <button
                key={opt.value}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => setMode(opt.value)}
                className={clsx(
                  "inline-flex h-6 items-center justify-center rounded px-2 transition-colors",
                  active
                    ? "bg-panel text-text"
                    : "text-muted hover:text-text",
                )}
              >
                {opt.label}
              </button>
            );
          })}
        </div>
      }
    />
  );
}

function TailBufferRow() {
  const [value, setValue] = useStoredString(TAIL_BUFFER_KEY, TAIL_BUFFER_DEFAULT);
  return (
    <SettingRow
      label="Tail buffer"
      hint="Max in-memory messages"
      control={
        <input
          type="number"
          min={100}
          step={100}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          aria-label="Tail buffer size"
          className="h-7 w-24 rounded-md border border-border bg-bg px-2 text-right font-mono text-[12px] tabular-nums text-text"
        />
      }
    />
  );
}

const CONFIRM_OPTIONS: { value: ConfirmDestructiveMode; label: string }[] = [
  { value: "always", label: "Always" },
  { value: "cluster", label: "Prod only" },
  { value: "never", label: "Never" },
];

function ConfirmDestructiveRow() {
  const [value, setValue] = useStoredString(
    CONFIRM_DESTRUCTIVE_KEY,
    CONFIRM_DESTRUCTIVE_DEFAULT,
  );
  const safe: ConfirmDestructiveMode =
    value === "cluster" || value === "never" ? value : "always";
  return (
    <SettingRow
      label="Confirm destructive"
      control={
        <select
          value={safe}
          onChange={(e) => setValue(e.target.value)}
          aria-label="Confirm destructive actions"
          className="h-7 rounded-md border border-border bg-bg px-2 text-[12px] text-text"
        >
          {CONFIRM_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      }
    />
  );
}

function SettingRow({
  label,
  hint,
  control,
}: {
  label: string;
  hint?: string;
  control: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-3 px-2.5 py-1.5">
      <div className="min-w-0">
        <div className="text-[12px] text-text">{label}</div>
        {hint ? <div className="text-[10px] text-muted">{hint}</div> : null}
      </div>
      <div className="shrink-0">{control}</div>
    </div>
  );
}
