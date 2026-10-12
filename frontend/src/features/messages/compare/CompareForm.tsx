import { useQuery } from "@tanstack/react-query";
import { ArrowLeftRight } from "lucide-react";
import { type FormEvent, useId, useState } from "react";
import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import { useFieldError } from "@/components/ui/FieldError";
import { Input } from "@/components/ui/Input";
import { TopicCombobox } from "@/features/messages/TopicCombobox";
import { parseFingerprint } from "@/lib/message-fingerprint";
import {
  formatMessageRef,
  type MessageRef,
  parseIndex,
  parseMessageRef,
} from "@/lib/message-reference";
import { topicQueries } from "@/lib/queries/topics";
import { useCluster } from "@/lib/use-cluster";

const labelCls = "mb-1 block text-xs font-semibold uppercase tracking-wider text-muted";
const TOPIC_RE = /^[a-zA-Z0-9._-]+$/;

interface SideDraft {
  cluster: string;
  topic: string;
  partition: string;
  offset: string;
}

function draftOf(ref: MessageRef | null, fallback?: MessageRef | null): SideDraft {
  if (ref) {
    return {
      cluster: ref.cluster,
      topic: ref.topic,
      partition: String(ref.partition),
      offset: String(ref.offset),
    };
  }
  return {
    cluster: fallback?.cluster ?? "",
    topic: fallback?.topic ?? "",
    partition: "",
    offset: "",
  };
}

function refOf(d: SideDraft): MessageRef | null {
  return parseMessageRef(`${d.cluster}/${d.topic}/${d.partition}/${d.offset}`);
}

/** Per-field problems of a draft; all null when it names a message. */
function draftErrors(d: SideDraft) {
  return {
    cluster: d.cluster ? null : "Select a cluster.",
    topic: TOPIC_RE.test(d.topic) ? null : "Enter a topic name.",
    partition: parseIndex(d.partition) === null ? "Enter a partition, 0 or higher." : null,
    offset: parseIndex(d.offset) === null ? "Enter an offset, 0 or higher." : null,
  };
}

/**
 * Picks the two messages: cluster, topic, partition and offset per side,
 * or a pasted reference that fills all four. Side B also takes a `kfp1:`
 * fingerprint instead of a message.
 */
export function CompareForm({
  a,
  b,
  onSubmit,
}: {
  /** Side A from the URL. */
  a?: string;
  /** Side B from the URL: a reference or a fingerprint. */
  b?: string;
  onSubmit: (a: string, b: string) => void;
}) {
  const swapHintId = useId();
  const refA = parseMessageRef(a ?? "");
  const [draftA, setDraftA] = useState(() => draftOf(refA));
  const [draftB, setDraftB] = useState(() => draftOf(parseMessageRef(b ?? ""), refA));
  const [fingerprintB, setFingerprintB] = useState(() => (b && parseFingerprint(b) ? b : ""));
  // Text typed into a reference field that is not (yet) a reference.
  const [pastedA, setPastedA] = useState<string | null>(null);
  const [pastedB, setPastedB] = useState<string | null>(null);
  const [submitted, setSubmitted] = useState(false);

  const validA = pastedA ? null : refOf(draftA);
  const validB = pastedB ? null : fingerprintB ? parseFingerprint(fingerprintB) : refOf(draftB);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    if (!validA || !validB) return;
    onSubmit(
      formatMessageRef(validA),
      fingerprintB ? fingerprintB.trim() : formatMessageRef(validB as MessageRef),
    );
  };

  const swap = () => {
    setDraftA(draftB);
    setDraftB(draftA);
    setPastedA(pastedB);
    setPastedB(pastedA);
  };

  return (
    <Card>
      <form onSubmit={submit} className="space-y-4" aria-label="Messages to compare">
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
          <SideFields
            side="A"
            draft={draftA}
            onDraft={setDraftA}
            pasted={pastedA}
            onPasted={setPastedA}
            submitted={submitted}
          />
          <SideFields
            side="B"
            draft={draftB}
            onDraft={setDraftB}
            fingerprint={fingerprintB}
            onFingerprint={setFingerprintB}
            pasted={pastedB}
            onPasted={setPastedB}
            submitted={submitted}
          />
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          {fingerprintB && (
            <p id={swapHintId} className="text-xs text-muted">
              Swap is unavailable while message B is a fingerprint.
            </p>
          )}
          <Button
            variant="ghost"
            size="sm"
            onClick={swap}
            disabled={!!fingerprintB}
            aria-describedby={fingerprintB ? swapHintId : undefined}
            leadingIcon={<ArrowLeftRight className="h-4 w-4" />}
          >
            Swap A and B
          </Button>
          <Button type="submit" size="sm">
            Compare
          </Button>
        </div>
      </form>
    </Card>
  );
}

function SideFields({
  side,
  draft,
  onDraft,
  fingerprint,
  onFingerprint,
  pasted,
  onPasted: setPasted,
  submitted,
}: {
  side: "A" | "B";
  draft: SideDraft;
  onDraft: (d: SideDraft) => void;
  fingerprint?: string;
  onFingerprint?: (f: string) => void;
  pasted: string | null;
  onPasted: (text: string | null) => void;
  submitted: boolean;
}) {
  const id = useId();
  const { clusters } = useCluster();
  const current = refOf(draft);
  const refText = pasted ?? (fingerprint || (current ? formatMessageRef(current) : ""));
  const topics = useQuery({
    ...topicQueries.list(draft.cluster),
    enabled: !!draft.cluster && !fingerprint,
    staleTime: 30_000,
  });

  const errors = submitted && !fingerprint ? draftErrors(draft) : null;
  const refField = useFieldError(
    submitted && pasted
      ? side === "B"
        ? "Not a reference (cluster/topic/partition/offset) or a kfp1: fingerprint."
        : "Not a reference: use cluster/topic/partition/offset."
      : null,
    `${id}-ref-hint`,
  );
  const clusterField = useFieldError(errors?.cluster);
  const topicField = useFieldError(errors?.topic);
  const partitionField = useFieldError(errors?.partition);
  const offsetField = useFieldError(errors?.offset);

  const onRefText = (text: string) => {
    const ref = parseMessageRef(text);
    if (ref) {
      onDraft(draftOf(ref));
      onFingerprint?.("");
      setPasted(null);
      return;
    }
    if (onFingerprint && parseFingerprint(text)) {
      onFingerprint(text.trim());
      setPasted(null);
      return;
    }
    onFingerprint?.("");
    setPasted(text || null);
  };

  const set = (patch: Partial<SideDraft>) => {
    setPasted(null);
    onDraft({ ...draft, ...patch });
  };

  // useCluster lists each name once (a shared cluster hides a private one
  // of the same name); a name from a pasted reference may be unknown.
  const clusterNames = (clusters ?? []).map((c) => c.name);
  if (draft.cluster && !clusterNames.includes(draft.cluster)) clusterNames.unshift(draft.cluster);

  return (
    <fieldset className="min-w-0 space-y-3">
      <legend className="mb-2 text-sm font-semibold text-text">Message {side}</legend>
      <div>
        <label htmlFor={`${id}-ref`} className={labelCls}>
          Reference
        </label>
        <Input
          id={`${id}-ref`}
          value={refText}
          onChange={(e) => onRefText(e.target.value)}
          placeholder={
            side === "B"
              ? "cluster/topic/partition/offset or kfp1:…"
              : "cluster/topic/partition/offset"
          }
          spellCheck={false}
          autoComplete="off"
          className="font-mono"
          invalid={refField.invalid}
          {...refField.controlProps}
        />
        <p id={`${id}-ref-hint`} className="mt-1 text-xs text-muted">
          Paste a reference (cluster/topic/partition/offset)
          {side === "B" ? " or a fingerprint" : ""}, or fill in the fields below.
        </p>
        {refField.message}
      </div>
      {fingerprint ? (
        <p className="text-sm text-muted">Message A is checked against this fingerprint.</p>
      ) : (
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label htmlFor={`${id}-cluster`} className={labelCls}>
              Cluster
            </label>
            <select
              id={`${id}-cluster`}
              value={draft.cluster}
              onChange={(e) => set({ cluster: e.target.value, topic: "" })}
              className={`h-9 w-full rounded-md border bg-panel px-3 text-sm text-text ${
                clusterField.invalid ? "border-danger" : "border-border hover:border-border-hover"
              }`}
              {...clusterField.controlProps}
            >
              <option value="" disabled>
                Select a cluster
              </option>
              {clusterNames.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
            {clusterField.message}
          </div>
          <div>
            <label htmlFor={`${id}-topic`} className={labelCls}>
              Topic
            </label>
            <TopicCombobox
              id={`${id}-topic`}
              value={draft.topic}
              onChange={(topic) => set({ topic })}
              topics={(topics.data ?? []).map((t) => t.name)}
              placeholder="topic-name"
              className={`h-9 w-full rounded-md border bg-panel px-3 font-mono text-sm text-text ${
                topicField.invalid ? "border-danger" : "border-border hover:border-border-hover"
              }`}
              {...topicField.controlProps}
            />
            {topicField.message}
          </div>
          <div>
            <label htmlFor={`${id}-partition`} className={labelCls}>
              Partition
            </label>
            <Input
              id={`${id}-partition`}
              value={draft.partition}
              onChange={(e) => set({ partition: e.target.value.trim() })}
              inputMode="numeric"
              autoComplete="off"
              className="font-mono tabular-nums"
              invalid={partitionField.invalid}
              {...partitionField.controlProps}
            />
            {partitionField.message}
          </div>
          <div>
            <label htmlFor={`${id}-offset`} className={labelCls}>
              Offset
            </label>
            <Input
              id={`${id}-offset`}
              value={draft.offset}
              onChange={(e) => set({ offset: e.target.value.trim() })}
              inputMode="numeric"
              autoComplete="off"
              className="font-mono tabular-nums"
              invalid={offsetField.invalid}
              {...offsetField.controlProps}
            />
            {offsetField.message}
          </div>
        </div>
      )}
    </fieldset>
  );
}
