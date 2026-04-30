import type { ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { clsx } from "clsx";

export function ErrorState({
  title,
  detail,
  onRetry,
  retryLabel = "Retry",
  className,
}: {
  title: ReactNode;
  detail?: ReactNode;
  onRetry?: () => void;
  retryLabel?: string;
  className?: string;
}) {
  return (
    <div
      className={clsx(
        "flex flex-col items-center justify-center rounded-xl border border-border bg-tint-red-bg px-6 py-12 text-center",
        className,
      )}
    >
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-danger text-panel">
        <AlertTriangle className="h-6 w-6" />
      </div>
      <h2 className="mt-4 text-base font-semibold text-tint-red-fg">{title}</h2>
      {detail && (
        <p className="mt-1 max-w-md text-xs text-tint-red-fg/80">{detail}</p>
      )}
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="mt-5 inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs font-semibold text-white transition-colors hover:bg-accent-hover"
        >
          ↻ {retryLabel}
        </button>
      )}
    </div>
  );
}
