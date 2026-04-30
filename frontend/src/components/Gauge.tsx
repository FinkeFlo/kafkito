import { clsx } from "clsx";

export function Gauge({
  v,
  hot = 70,
  width = 80,
  className,
}: {
  v: number;
  hot?: number;
  width?: number;
  className?: string;
}) {
  const clamped = Math.max(0, Math.min(100, v));
  const isHot = v >= hot;
  return (
    <div className={clsx("flex items-center gap-2", className)}>
      <div
        className="h-1.5 rounded-full bg-subtle"
        style={{ width: `${width}px` }}
      >
        <div
          className={clsx(
            "h-full rounded-full transition-[width] duration-150",
            isHot ? "bg-warning" : "bg-accent",
          )}
          style={{ width: `${clamped}%` }}
        />
      </div>
      <span
        className={clsx(
          "font-mono text-[12px] tabular-nums",
          isHot ? "text-warning" : "text-muted",
        )}
      >
        {Math.round(v)}%
      </span>
    </div>
  );
}
