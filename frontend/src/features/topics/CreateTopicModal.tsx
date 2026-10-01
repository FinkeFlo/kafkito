import { useId, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { toast } from "sonner";
import { createTopic } from "@/lib/api";
import { Button } from "@/components/ui/Button";
import { useFieldError } from "@/components/ui/FieldError";
import { IconButton } from "@/components/ui/IconButton";
import { Input } from "@/components/ui/Input";
import { Modal } from "@/components/ui/Modal";
import { Notice } from "@/components/ui/Notice";
import { topicQueries } from "@/lib/queries/topics";
import { topicNameError } from "./topic-name";

const LABEL = "text-xs font-semibold uppercase tracking-wider text-muted";
const HINT = "mt-1 block text-xs text-subtle-text";

/** Parses a positive whole number typed into a field; null when it is not one. */
function positiveInt(raw: string): number | null {
  const t = raw.trim();
  if (!/^\d+$/.test(t)) return null;
  const n = Number(t);
  return n >= 1 ? n : null;
}

interface ConfigRow {
  id: number;
  k: string;
  v: string;
}

export function CreateTopicModal({
  cluster,
  brokers,
  onClose,
}: {
  cluster: string;
  /** Broker count when known; caps the replication factor. */
  brokers?: number;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [partitions, setPartitions] = useState("1");
  const [rf, setRF] = useState("1");
  const [configRows, setConfigRows] = useState<ConfigRow[]>([]);
  const nextRowId = useRef(0);
  const [submitted, setSubmitted] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const nameError = topicNameError(name);
  const partitionsValue = positiveInt(partitions);
  const rfValue = positiveInt(rf);
  const rfError =
    rfValue === null
      ? "Enter a whole number of at least 1."
      : brokers !== undefined && rfValue > brokers
        ? `Use at most ${brokers}.`
        : null;
  const ready = !nameError && partitionsValue !== null && !rfError;

  // An empty name is only an error once the user tried to submit; every
  // other rule is shown as soon as it is broken.
  const partitionsHintId = useId();
  const rfHintId = useId();
  const nameField = useFieldError(name.trim() || submitted ? nameError : null);
  const partitionsField = useFieldError(
    partitionsValue === null ? "Enter a whole number of at least 1." : null,
    partitionsHintId,
  );
  const rfField = useFieldError(rfError, brokers !== undefined ? rfHintId : undefined);

  const mut = useMutation({
    mutationFn: async () => {
      const configs: Record<string, string> = {};
      for (const { k, v } of configRows) {
        if (k.trim()) configs[k.trim()] = v;
      }
      await createTopic(cluster, {
        name: name.trim(),
        partitions: partitionsValue ?? 1,
        replication_factor: rfValue ?? 1,
        configs: Object.keys(configs).length ? configs : undefined,
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: topicQueries.list(cluster).queryKey });
      toast.success(`Topic "${name.trim()}" created`);
      onClose();
    },
    onError: (e: Error) => setErr(e.message),
  });

  const submit = () => {
    setSubmitted(true);
    if (!ready || mut.isPending) return;
    setErr(null);
    mut.mutate();
  };

  const updateRow = (id: number, patch: Partial<ConfigRow>) =>
    setConfigRows((rows) => rows.map((r) => (r.id === id ? { ...r, ...patch } : r)));

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      onSubmit={submit}
      title={
        <span className="flex flex-col">
          <span className="text-[11px] font-semibold uppercase tracking-wider text-muted">
            Create topic on
          </span>
          <span className="font-mono text-[13px] font-semibold">{cluster}</span>
        </span>
      }
      actions={
        <>
          {partitionsValue !== null && rfValue !== null ? (
            <span className="mr-auto text-xs tabular-nums text-muted">
              {partitionsValue} {partitionsValue === 1 ? "partition" : "partitions"} × RF {rfValue}{" "}
              = {partitionsValue * rfValue}{" "}
              {partitionsValue * rfValue === 1 ? "replica" : "replicas"}
            </span>
          ) : null}
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="primary"
            size="sm"
            disabled={!ready}
            loading={mut.isPending}
          >
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div>
          <label htmlFor="create-topic-name" className={LABEL}>
            Name
          </label>
          <Input
            id="create-topic-name"
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="my-topic"
            className="mt-1 font-mono"
            autoComplete="off"
            spellCheck={false}
            {...nameField.controlProps}
          />
          {nameField.message}
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label htmlFor="create-topic-partitions" className={LABEL}>
              Partitions
            </label>
            <Input
              id="create-topic-partitions"
              inputMode="numeric"
              value={partitions}
              onChange={(e) => setPartitions(e.target.value)}
              className="mt-1 tabular-nums"
              {...partitionsField.controlProps}
            />
            <span id={partitionsHintId} className={HINT}>
              Can be increased later, never decreased.
            </span>
            {partitionsField.message}
          </div>
          <div>
            <label htmlFor="create-topic-rf" className={LABEL}>
              Replication factor
            </label>
            <Input
              id="create-topic-rf"
              inputMode="numeric"
              value={rf}
              onChange={(e) => setRF(e.target.value)}
              className="mt-1 tabular-nums"
              {...rfField.controlProps}
            />
            {brokers !== undefined ? (
              <span id={rfHintId} className={HINT}>
                The cluster has {brokers} {brokers === 1 ? "broker" : "brokers"}.
              </span>
            ) : null}
            {rfField.message}
          </div>
        </div>
        <div>
          <div className="mb-1 flex items-center justify-between">
            <span className={LABEL}>
              Configs <span className="font-normal normal-case tracking-normal">(optional)</span>
            </span>
            <Button
              variant="ghost"
              size="sm"
              leadingIcon={<Plus className="h-4 w-4" aria-hidden />}
              onClick={() =>
                setConfigRows((rows) => [...rows, { id: nextRowId.current++, k: "", v: "" }])
              }
            >
              Add config
            </Button>
          </div>
          {configRows.length === 0 ? (
            <div className="rounded-md border border-dashed border-border p-2 text-center text-xs text-subtle-text">
              No overrides, broker defaults apply
            </div>
          ) : (
            <div className="space-y-1.5">
              <div
                aria-hidden="true"
                className="grid grid-cols-[1fr_1fr_2rem] gap-2 text-[11px] font-semibold uppercase tracking-wider text-subtle-text"
              >
                <span>Key</span>
                <span>Value</span>
              </div>
              {configRows.map((row, i) => (
                <div key={row.id} className="grid grid-cols-[1fr_1fr_2rem] gap-2">
                  <Input
                    aria-label={`Config ${i + 1} key`}
                    value={row.k}
                    onChange={(e) => updateRow(row.id, { k: e.target.value })}
                    placeholder="retention.ms"
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                  />
                  <Input
                    aria-label={`Config ${i + 1} value`}
                    value={row.v}
                    onChange={(e) => updateRow(row.id, { v: e.target.value })}
                    placeholder="604800000"
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                  />
                  <IconButton
                    aria-label={`Remove config ${row.k.trim() || i + 1}`}
                    icon={<X className="h-4 w-4" />}
                    variant="danger"
                    size="sm"
                    className="self-center"
                    onClick={() => setConfigRows((rows) => rows.filter((r) => r.id !== row.id))}
                  />
                </div>
              ))}
            </div>
          )}
        </div>
        {err && <Notice intent="danger">{err}</Notice>}
      </div>
    </Modal>
  );
}
