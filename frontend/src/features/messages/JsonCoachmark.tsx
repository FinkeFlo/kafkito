/** One-time tip that JSON values in a row can be clicked to filter by them. */
export function JsonCoachmark({ onDismiss }: { onDismiss: () => void }) {
  return (
    <div className="m-3 flex items-center gap-2 rounded border border-accent/40 bg-accent-subtle p-2 text-xs text-accent">
      <span>Tip: click any value in a JSON message to filter by it.</span>
      <button
        type="button"
        onClick={onDismiss}
        className="ml-auto rounded border border-border px-2 py-0.5 hover:border-border-strong"
      >
        Got it
      </button>
    </div>
  );
}
