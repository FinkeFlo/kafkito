import { useNavigate } from "@tanstack/react-router";
import { Check } from "lucide-react";
import { sameMessage, setCompareSelection, useCompareSelection } from "@/lib/compare-selection";
import { formatMessageRef, type MessageRef } from "@/lib/message-reference";

// Matches the hand-styled buttons of the message row (DESIGN_GUIDELINES § 14).
const rowButton =
  "rounded border border-border px-2 py-1 text-[11px] text-text hover:border-border-hover";
const rowPrimaryButton =
  "rounded border border-accent bg-accent px-2 py-1 text-[11px] font-medium text-accent-foreground hover:bg-accent-hover";

/**
 * The compare action of one message: "Select for compare" remembers it as
 * message A; on any other message "Compare with selected" opens the compare
 * page with both. The selection outlives topic and cluster switches (see
 * lib/compare-selection.ts and the compare bar).
 */
export function CompareAction({ target }: { target: MessageRef }) {
  const selected = useCompareSelection();
  const navigate = useNavigate();

  if (!selected) {
    return (
      <button
        type="button"
        onClick={() => setCompareSelection(target)}
        className={rowButton}
        title="Remember this message, then open another one to compare them"
      >
        Select for compare
      </button>
    );
  }
  if (sameMessage(selected, target)) {
    return (
      <span className="inline-flex items-center gap-1 text-[11px] text-muted">
        <Check aria-hidden="true" className="h-3.5 w-3.5" />
        Selected for compare
      </span>
    );
  }
  return (
    <>
      <button
        type="button"
        onClick={() => setCompareSelection(target)}
        className={rowButton}
        title="Make this message the one to compare against"
      >
        Select this instead
      </button>
      <button
        type="button"
        onClick={() =>
          void navigate({
            to: "/compare",
            search: { a: formatMessageRef(selected), b: formatMessageRef(target) },
            // The compare page's Close button returns here.
            state: (prev) => ({ compareOrigin: prev.__TSR_index }),
          })
        }
        className={rowPrimaryButton}
        title={`Compare ${formatMessageRef(selected)} with this message`}
      >
        Compare with selected
      </button>
    </>
  );
}
