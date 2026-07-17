import { useState } from "react";
import { Button } from "@/components/button";
import { Modal } from "@/components/Modal";
import {
  CHANGELOG,
  type ChangelogEntry,
  type ChangelogItemType,
} from "@/content/changelog";
import { normalizeVersion } from "@/lib/whats-new";

// Token-only badge styling. Refine per DESIGN_GUIDELINES if needed; keep
// to @theme tokens (check:palette blocks default-palette classes).
const BADGE: Record<ChangelogItemType, { label: string; cls: string }> = {
  feature: { label: "Feature", cls: "border border-border text-accent" },
  fix: { label: "Fix", cls: "border border-border text-muted" },
  security: { label: "Security", cls: "border border-border text-danger" },
};

function EntryBlock({
  entry,
  defaultOpen,
}: {
  entry: ChangelogEntry;
  defaultOpen: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <section className="border-b border-border py-3 last:border-b-0">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center justify-between text-left"
        aria-expanded={open}
      >
        <span className="font-mono text-sm font-semibold text-text">
          v{entry.version}
        </span>
        <span className="text-xs text-subtle-text">{entry.date}</span>
      </button>
      {open && (
        <ul className="mt-2 grid grid-cols-[auto_1fr] items-start gap-x-3 gap-y-2 text-sm">
          {entry.items.map((it, i) => (
            <li key={i} className="contents">
              <span
                className={`mt-0.5 justify-self-start rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${BADGE[it.type].cls}`}
              >
                {BADGE[it.type].label}
              </span>
              <div>
                <span className="font-medium text-text">{it.title}</span>
                {it.description ? (
                  <span className="mt-0.5 block text-muted">
                    {it.description}
                  </span>
                ) : null}
                {it.screenshot ? (
                  <img
                    src={it.screenshot.src}
                    alt={it.screenshot.alt}
                    loading="lazy"
                    className="mt-2 max-h-44 w-auto rounded border border-border"
                  />
                ) : null}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export function WhatsNewModal({
  currentVersion,
  onClose,
}: {
  currentVersion?: string;
  onClose: () => void;
}) {
  const current = currentVersion ? normalizeVersion(currentVersion) : undefined;
  const matchIdx = current
    ? CHANGELOG.findIndex((e) => e.version === current)
    : -1;
  return (
    <Modal
      open
      size="lg"
      onClose={onClose}
      title="What's new"
      actions={
        <Button variant="ghost" size="sm" onClick={onClose}>
          Close
        </Button>
      }
    >
      {CHANGELOG.length === 0 ? (
        <p className="text-sm text-muted">No release notes available.</p>
      ) : (
        CHANGELOG.map((entry, i) => (
          <EntryBlock
            key={entry.version}
            entry={entry}
            defaultOpen={matchIdx >= 0 ? i === matchIdx : i === 0}
          />
        ))
      )}
    </Modal>
  );
}
