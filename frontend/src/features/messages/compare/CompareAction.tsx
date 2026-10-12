import { useNavigate } from "@tanstack/react-router";
import { Check } from "lucide-react";
import { sameMessage, setCompareSelection, useCompareSelection } from "@/lib/compare-selection";
import { formatMessageRef, type MessageRef } from "@/lib/message-reference";

// Matches the hand-styled buttons of the message row (DESIGN_GUIDELINES § 14).
const rowButton =
  "rounded border border-border px-2 py-1 text-[11px] text-text hover:border-border-hover";

/**
 * The compare action of one message: "Select for compare" remembers it as
 * message A; on any other message "Compare with selected" opens the compare
 * page with both. The selection outlives topic and cluster switches (see
 * lib/compare-selection.ts and the compare bar, which announces changes).
 *
 * The select / clear button keeps its place in every state, so it keeps
 * keyboard focus when a click changes the state: the status text sits in
 * front of it in a slot that is always rendered.
 */
export function CompareAction({ target }: { target: MessageRef }) {
  const selected = useCompareSelection();
  const navigate = useNavigate();
  const isSelected = sameMessage(selected, target);
  const otherSelected = !!selected && !isSelected;

  return (
    <>
      <span className="inline-flex items-center gap-1 text-[11px] text-muted">
        {isSelected && (
          <>
            <Check aria-hidden="true" className="h-3.5 w-3.5" />
            Selected for compare
          </>
        )}
      </span>
      <button
        type="button"
        onClick={() => setCompareSelection(isSelected ? null : target)}
        className={rowButton}
        // The compare bar's Clear moves focus here when the row is open.
        data-compare-target={formatMessageRef(target)}
        title={
          isSelected
            ? "Forget this message as the one to compare against"
            : otherSelected
              ? "Make this message the one to compare against"
              : "Remember this message, then open another one to compare them"
        }
      >
        {isSelected
          ? "Clear selection"
          : otherSelected
            ? "Select this instead"
            : "Select for compare"}
      </button>
      {otherSelected && (
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
          className={`${rowButton} font-medium`}
          title={`Compare ${formatMessageRef(selected)} with this message`}
        >
          Compare with selected
        </button>
      )}
    </>
  );
}
