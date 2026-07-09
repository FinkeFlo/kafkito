import { useEffect, useState, useSyncExternalStore } from "react";
import { useQuery } from "@tanstack/react-query";
import { Sparkles } from "lucide-react";
import { fetchInfo } from "@/lib/api";
import { anyModalOpen } from "@/components/Modal";
import { WhatsNewModal } from "@/components/whats-new-modal";
import {
  getLastSeen,
  hasUnseen,
  markSeen,
  normalizeVersion,
  subscribeWhatsNew,
} from "@/lib/whats-new";

export function WhatsNewButton() {
  const infoQuery = useQuery({
    queryKey: ["info"],
    queryFn: fetchInfo,
    staleTime: 5 * 60_000,
  });
  const current = infoQuery.data?.version
    ? normalizeVersion(infoQuery.data.version)
    : undefined;

  const lastSeen = useSyncExternalStore(
    subscribeWhatsNew,
    getLastSeen,
    () => null,
  );
  const unseen = hasUnseen(current, lastSeen);

  const [open, setOpen] = useState(false);
  const [autoOpened, setAutoOpened] = useState(false);

  // Auto-open once per load when the running version is unseen — unless a
  // modal is already open (don't stack; the dot remains the entry point).
  useEffect(() => {
    if (unseen && !autoOpened && !anyModalOpen()) {
      setAutoOpened(true);
      setOpen(true);
    }
  }, [unseen, autoOpened]);

  // Opening the panel (auto or manual) acknowledges the current version.
  useEffect(() => {
    if (open && current) markSeen(current);
  }, [open, current]);

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label="What's new"
        title="What's new"
        className="relative flex h-8 w-8 items-center justify-center rounded-md border border-border bg-panel text-muted transition-colors hover:bg-hover"
      >
        <Sparkles className="h-4 w-4" />
        {unseen ? (
          <span
            aria-hidden
            className="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full bg-accent"
          />
        ) : null}
      </button>
      {open ? (
        <WhatsNewModal
          currentVersion={infoQuery.data?.version}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </>
  );
}
