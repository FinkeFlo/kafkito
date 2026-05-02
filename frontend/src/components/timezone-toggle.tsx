import { Clock, Globe } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useTimeZone, type TimeZoneMode } from "@/lib/use-timezone";

const order: TimeZoneMode[] = ["utc", "local"];

const icons: Record<TimeZoneMode, typeof Clock> = {
  utc: Globe,
  local: Clock,
};

export function TimezoneToggle() {
  const { t } = useTranslation("common");
  const [mode, setMode] = useTimeZone();
  const Icon = icons[mode];
  const label = t(`timezone.${mode}`);

  const next = () => {
    const idx = order.indexOf(mode);
    setMode(order[(idx + 1) % order.length]);
  };

  return (
    <button
      type="button"
      onClick={next}
      title={`${label} (${t("timezone.cycleHint")})`}
      aria-label={label}
      className="flex h-8 items-center gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-subtle)] px-2 text-xs font-medium text-[var(--color-text-muted)] transition-colors hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]"
    >
      <Icon className="h-4 w-4" />
      <span className="uppercase tracking-wide">{mode}</span>
    </button>
  );
}
