import { createFileRoute, useRouter, useRouterState } from "@tanstack/react-router";
import { GitCompareArrows, X } from "lucide-react";
import { useMemo } from "react";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { PageHeader } from "@/components/ui/PageHeader";
import { SegmentedControl } from "@/components/ui/SegmentedControl";
import { Toolbar } from "@/components/ui/Toolbar";
import { CompareForm } from "@/features/messages/compare/CompareForm";
import { ComparePanel, FingerprintPanel } from "@/features/messages/compare/ComparePanel";
import {
  type CompareSearch,
  type CompareView,
  compareOptions,
  compareView,
  messageListLink,
  optionsSearch,
  parseCompareSearch,
} from "@/features/messages/compare/compare-search";
import { describeFingerprintOptions } from "@/features/messages/compare/FingerprintCheck";
import type { CompareOptions } from "@/lib/message-compare";
import { parseFingerprint } from "@/lib/message-fingerprint";
import { formatMessageRef, parseMessageRef } from "@/lib/message-reference";

export const Route = createFileRoute("/compare")({
  validateSearch: parseCompareSearch,
  component: ComparePage,
});

const VIEW_OPTIONS: { value: CompareView; content: string; title: string }[] = [
  { value: "semantic", content: "Semantic", title: "JSON and XML in canonical form" },
  { value: "exact", content: "Exact", title: "As stored, byte for byte" },
];

function ComparePage() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const router = useRouter();
  const historyState = useRouterState({ select: (s) => s.location.state });
  const view = compareView(search);
  const { anyArrayOrder, anyElementOrder, kafkitoHeaders } = search;
  // Stable across renders so the comparison is only recomputed on a change.
  const options = useMemo(
    () => compareOptions({ anyArrayOrder, anyElementOrder, kafkitoHeaders }),
    [anyArrayOrder, anyElementOrder, kafkitoHeaders],
  );

  const refA = parseMessageRef(search.a ?? "");
  const refB = parseMessageRef(search.b ?? "");
  const fingerprintB = refB ? null : parseFingerprint(search.b ?? "");

  // Every navigation on this page keeps the index of the page it was
  // opened from, so Close can return there after any number of compares.
  const update = (patch: Partial<CompareSearch>, replace = true) =>
    void navigate({
      search: (prev: CompareSearch) => ({ ...prev, ...patch }),
      replace,
      state: (prev) => ({ compareOrigin: prev.compareOrigin }),
    });
  const close = () => {
    const origin = historyState.compareOrigin;
    const current = historyState.__TSR_index;
    if (origin !== undefined && current > origin) {
      router.history.go(origin - current);
    } else if (refA) {
      // Opened from a bookmark or a pasted link: nothing in this tab to go back to.
      void navigate(messageListLink(refA));
    } else {
      void navigate({ to: "/clusters", search: { cluster: undefined } });
    }
  };
  const setOptions = (next: CompareOptions, nextView: CompareView = view) =>
    update(optionsSearch(next, nextView));
  const takeOffset = (side: "A" | "B", offset: number) => {
    const ref = side === "A" ? refA : refB;
    if (!ref) return;
    const next = formatMessageRef({ ...ref, offset });
    update(side === "A" ? { a: next } : { b: next });
  };

  return (
    <div className="space-y-5 p-6">
      <PageHeader
        eyebrow="Messages"
        title="Compare messages"
        subtitle="Check whether two records match: exactly as stored, or semantically, with JSON key order and XML attribute order ignored."
        actions={
          <Button
            variant="secondary"
            size="sm"
            onClick={close}
            leadingIcon={<X className="h-4 w-4" />}
          >
            Close
          </Button>
        }
      />

      <CompareForm
        key={`${search.a ?? ""}|${search.b ?? ""}`}
        a={search.a}
        b={search.b}
        onSubmit={(a, b) => update({ a, b }, false)}
      />

      {fingerprintB ? (
        <p className="text-sm text-muted">
          Settings come from the pasted fingerprint:{" "}
          {describeFingerprintOptions(fingerprintB.options)}.
        </p>
      ) : (
        <Toolbar
          filters={
            <>
              <SegmentedControl
                label="Diff view"
                value={view}
                onChange={(v) => setOptions(options, v)}
                options={VIEW_OPTIONS}
                className="text-xs font-medium"
                itemClassName="px-3"
              />
              <OptionCheckbox
                checked={options.ignoreArrayOrder}
                onChange={(v) => setOptions({ ...options, ignoreArrayOrder: v })}
                label="Ignore JSON array order"
              />
              <OptionCheckbox
                checked={options.ignoreChildOrder}
                onChange={(v) => setOptions({ ...options, ignoreChildOrder: v })}
                label="Ignore XML element order"
              />
              <OptionCheckbox
                checked={options.ignoreKafkitoHeaders}
                onChange={(v) => setOptions({ ...options, ignoreKafkitoHeaders: v })}
                label="Ignore X-Kafkito-* headers"
              />
              <OptionCheckbox
                checked={search.onlyDiff === true}
                onChange={(v) => update({ onlyDiff: v ? true : undefined })}
                label="Only differences"
              />
            </>
          }
        />
      )}

      {!refA || (!refB && !fingerprintB) ? (
        <EmptyState
          icon={GitCompareArrows}
          title="Pick two messages"
          description="Open a message in a topic and choose Select for compare, then Compare with selected on another message. Or enter a cluster, topic, partition and offset for each side above."
        />
      ) : fingerprintB ? (
        <FingerprintPanel
          refA={refA}
          fingerprint={search.b ?? ""}
          options={fingerprintB.options}
          onUseOffset={takeOffset}
        />
      ) : (
        refB && (
          <ComparePanel
            refA={refA}
            refB={refB}
            options={options}
            view={view}
            onlyDiff={search.onlyDiff === true}
            onUseOffset={takeOffset}
          />
        )
      )}
    </div>
  );
}

function OptionCheckbox({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
}) {
  return (
    <label className="flex cursor-pointer items-center gap-1.5 text-xs text-text">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="h-3.5 w-3.5 accent-accent"
      />
      {label}
    </label>
  );
}
