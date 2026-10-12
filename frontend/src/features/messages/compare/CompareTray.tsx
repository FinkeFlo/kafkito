import { useNavigate, useRouterState } from "@tanstack/react-router";
import { GitCompareArrows } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/Button";
import { setCompareSelection, useCompareSelection } from "@/lib/compare-selection";
import { formatMessageRef } from "@/lib/message-reference";
import { useFormatters } from "@/lib/use-formatters";

/**
 * Bar at the bottom of every page while a message is selected for compare:
 * names the selection and says how to go on, so the selection is never
 * invisible state. Hidden on the compare page, which shows both messages.
 */
export function CompareTray() {
  const selected = useCompareSelection();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const navigate = useNavigate();
  const fmt = useFormatters();
  const [copied, setCopied] = useState(false);

  if (!selected || pathname === "/compare") return null;
  const reference = formatMessageRef(selected);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(reference);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore clipboard errors (permissions, insecure context)
    }
  };

  return (
    <aside
      aria-label="Message selected for compare"
      data-testid="compare-tray"
      className="sticky bottom-0 z-10 border-t border-border bg-panel px-6 py-3"
    >
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <GitCompareArrows aria-hidden="true" className="h-4 w-4 shrink-0 text-accent" />
        <div className="min-w-0 flex-1 text-sm">
          <div className="text-text">
            Selected for compare:{" "}
            <span className="font-mono">
              {selected.cluster} / {selected.topic} · p{selected.partition} · #
              {fmt.number(selected.offset)}
            </span>
          </div>
          <div className="text-xs text-muted">
            Open another message and choose Compare with selected, or enter message B by hand.
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="secondary"
            size="sm"
            onClick={() =>
              void navigate({
                to: "/compare",
                search: { a: reference },
                state: (prev) => ({ compareOrigin: prev.__TSR_index }),
              })
            }
          >
            Enter message B
          </Button>
          <Button variant="ghost" size="sm" onClick={copy}>
            {copied ? "Copied" : "Copy reference"}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setCompareSelection(null)}>
            Clear
          </Button>
        </div>
      </div>
    </aside>
  );
}
