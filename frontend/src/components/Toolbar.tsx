import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface ToolbarProps {
  /** Typically a `<SearchInput />` — left-aligned. */
  search?: ReactNode;
  /** Filter dropdowns / checkboxes — sits next to search. */
  filters?: ReactNode;
  /** Buttons or icon buttons — pushed to the right via `ml-auto`. */
  actions?: ReactNode;
  className?: string;
}

/**
 * Filter / action row that sits above data-dense surfaces. Replaces the
 * hand-rolled `<div className="flex flex-wrap items-center gap-2">` blocks
 * scattered across `routes/topics.tsx`, `routes/groups.tsx`, and
 * `routes/index.tsx`.
 */
export function Toolbar({ search, filters, actions, className }: ToolbarProps) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-center gap-2",
        className,
      )}
    >
      {search ? <div className="flex min-w-[260px] flex-1 items-center">{search}</div> : null}
      {filters ? (
        <div className="flex flex-wrap items-center gap-2">{filters}</div>
      ) : null}
      {actions ? (
        <div className="ml-auto flex flex-wrap items-center gap-2">{actions}</div>
      ) : null}
    </div>
  );
}
