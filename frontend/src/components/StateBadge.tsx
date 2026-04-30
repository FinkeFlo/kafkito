import { clsx } from "clsx";

type KnownState =
  | "stable"
  | "empty"
  | "preparingrebalance"
  | "completingrebalance"
  | "dead"
  | "unknown";

function normalize(state: string): KnownState {
  const s = state.toLowerCase().replace(/[\s_-]+/g, "");
  if (s === "stable") return "stable";
  if (s === "empty") return "empty";
  if (s === "preparingrebalance") return "preparingrebalance";
  if (s === "completingrebalance") return "completingrebalance";
  if (s === "dead") return "dead";
  return "unknown";
}

const styles: Record<KnownState, string> = {
  stable: "bg-tint-green-bg text-tint-green-fg",
  empty: "bg-subtle text-muted",
  preparingrebalance: "bg-tint-amber-bg text-tint-amber-fg",
  completingrebalance: "bg-tint-amber-bg text-tint-amber-fg",
  dead: "bg-tint-red-bg text-tint-red-fg",
  unknown: "bg-subtle text-muted",
};

export function StateBadge({
  state,
  className,
}: {
  state: string;
  className?: string;
}) {
  const key = normalize(state);
  return (
    <span
      className={clsx(
        "inline-flex items-center rounded-sm px-1.5 py-0.5 text-[11px] font-medium",
        styles[key],
        className,
      )}
    >
      {state}
    </span>
  );
}
