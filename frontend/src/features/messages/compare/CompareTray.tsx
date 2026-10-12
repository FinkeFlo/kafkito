import { useMatchRoute, useNavigate } from "@tanstack/react-router";
import { GitCompareArrows } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/Button";
import {
  getCompareSelection,
  setCompareSelection,
  useCompareSelection,
} from "@/lib/compare-selection";
import { formatMessageRef, type MessageRef } from "@/lib/message-reference";
import { useFormatters } from "@/lib/use-formatters";

/** Root custom property the toaster and scroll padding read the bar height from. */
const BAR_HEIGHT_VAR = "--compare-bar-height";

/**
 * Bar at the bottom of every page while a message is selected for compare:
 * names the selection and says how to go on, so the selection is never
 * invisible state. Hidden on the compare page, which shows both messages.
 * Its live region, always mounted, announces selection changes made here or
 * on a message row.
 */
export function CompareTray() {
  const selected = useCompareSelection();
  const matchRoute = useMatchRoute();
  const onComparePage = !!matchRoute({ to: "/compare" });
  const [announcement, setAnnouncement] = useState("");
  const fmt = useFormatters();

  const label = selected ? describe(selected, fmt.number) : "";
  const previous = useRef(label);
  useEffect(() => {
    if (label === previous.current) return;
    previous.current = label;
    setAnnouncement(
      label
        ? `Selected for compare: ${label}. Open another message and choose Compare with selected.`
        : "Compare selection cleared.",
    );
  }, [label]);

  return (
    <>
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>
      {selected && !onComparePage && (
        <Bar selected={selected} label={label} onAnnounce={setAnnouncement} />
      )}
    </>
  );
}

function describe(ref: MessageRef, number: (n: number) => string): string {
  return `${ref.cluster} / ${ref.topic} · p${ref.partition} · #${number(ref.offset)}`;
}

function Bar({
  selected,
  label,
  onAnnounce,
}: {
  selected: MessageRef;
  label: string;
  onAnnounce: (text: string) => void;
}) {
  const navigate = useNavigate();
  const barRef = useRef<HTMLElement>(null);
  const [copied, setCopied] = useState(false);
  const reference = formatMessageRef(selected);

  // While the bar covers the bottom of the viewport, keep focused controls
  // and toasts clear of it.
  useLayoutEffect(() => {
    const el = barRef.current;
    const root = document.documentElement;
    if (!el) return;
    const apply = () => {
      const h = `${el.offsetHeight}px`;
      root.style.setProperty(BAR_HEIGHT_VAR, h);
      root.style.scrollPaddingBottom = h;
    };
    apply();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(apply);
    observer?.observe(el);
    return () => {
      observer?.disconnect();
      root.style.removeProperty(BAR_HEIGHT_VAR);
      root.style.scrollPaddingBottom = "";
    };
  }, []);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(reference);
      setCopied(true);
      onAnnounce("Reference copied.");
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // ignore clipboard errors (permissions, insecure context)
    }
  };

  const clear = () => {
    const text = getCompareSelection();
    setCompareSelection(null);
    // The bar disappears with its focused button. Hand focus to the selected
    // message's own compare button when its row is open, else to the page.
    const target = text
      ? document.querySelector<HTMLElement>(`[data-compare-target="${CSS.escape(text)}"]`)
      : null;
    (target ?? document.getElementById("main-content"))?.focus();
  };

  return (
    <aside
      ref={barRef}
      aria-label="Message selected for compare"
      data-testid="compare-tray"
      className="sticky bottom-0 border-t border-border bg-panel px-6 py-3"
    >
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <GitCompareArrows aria-hidden="true" className="h-4 w-4 shrink-0 text-accent" />
        <div className="min-w-0 flex-1 text-sm">
          <div className="text-text">
            Selected for compare: <span className="font-mono tabular-nums">{label}</span>
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
          <Button variant="ghost" size="sm" onClick={clear}>
            Clear
          </Button>
        </div>
      </div>
    </aside>
  );
}
