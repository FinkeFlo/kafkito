import { createFileRoute } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { History, Plus, Trash2 } from "lucide-react";
import {
  fetchMessages,
  produceMessage,
  can,
  type Message,
  type PartitionInfo,
  type ProduceResult,
} from "@/lib/api";
import { useAuth } from "@/auth/hooks";
import { Button } from "@/components/ui/Button";
import { IconButton } from "@/components/ui/IconButton";
import { Card } from "@/components/ui/Card";
import { Input } from "@/components/ui/Input";
import { useFieldError } from "@/components/ui/FieldError";
import { Timestamp } from "@/components/ui/Timestamp";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { useCluster } from "@/lib/use-cluster";
import { StatusBox } from "@/components/ui/StatusIcon";
import { messageKeys, messageQueries } from "@/lib/queries/messages";
import { topicQueries } from "@/lib/queries/topics";

export const Route = createFileRoute("/clusters/$cluster/topics/$topic/produce")({
  component: ProduceTab,
});

function ProduceTab() {
  const { cluster, topic } = Route.useParams();

  const detailQuery = useQuery({
    ...topicQueries.detail(cluster, topic),
    enabled: !!cluster,
    refetchInterval: 5_000,
  });

  if (!detailQuery.data) {
    return <div className="text-sm text-muted">Loading…</div>;
  }

  return (
    <ProduceSection cluster={cluster} topic={topic} partitions={detailQuery.data.partitions} />
  );
}

function ProduceSection({
  cluster,
  topic,
  partitions,
}: {
  cluster: string;
  topic: string;
  partitions: PartitionInfo[];
}) {
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [partition, setPartition] = useState<string>("auto");
  const [headers, setHeaders] = useState<{ k: string; v: string }[]>([]);
  const [valueFormat, setValueFormat] = useState<"text" | "json">("text");
  const [busy, setBusy] = useState(false);
  const [templating, setTemplating] = useState(false);
  const [templateError, setTemplateError] = useState<string | null>(null);
  const [result, setResult] = useState<ProduceResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [valueError, setValueError] = useState<string | null>(null);
  const valueField = useFieldError(valueError);
  const [confirmProdOpen, setConfirmProdOpen] = useState(false);
  const qc = useQueryClient();
  const { clusters } = useCluster();
  const isProdCluster = !!clusters?.find((c) => c.name === cluster)?.is_prod;

  const addHeader = () => setHeaders((h) => [...h, { k: "", v: "" }]);
  const removeHeader = (i: number) => setHeaders((h) => h.filter((_, idx) => idx !== i));
  const updateHeader = (i: number, field: "k" | "v", val: string) =>
    setHeaders((h) => h.map((row, idx) => (idx === i ? { ...row, [field]: val } : row)));
  const { me } = useAuth();
  const rbacAllowsProduce = can(me, cluster, "topic", "produce", topic);
  const rbacAllowsConsume = can(me, cluster, "topic", "consume", topic);

  // Probe across all partitions for the most recent record. We pull a small
  // window from each partition's tail and pick the highest timestamp — the
  // latest record may live in any partition, not just partition 0.
  const latestProbeQuery = useQuery({
    ...messageQueries.latestProbe(cluster, topic),
    enabled: !!cluster && rbacAllowsConsume,
    staleTime: 5_000,
  });
  const latestProbeAvailable = !!latestProbeQuery.data && latestProbeQuery.data.messages.length > 0;

  const looksLikeJSON = (s: string): boolean => {
    const trimmed = s.trim();
    if (!trimmed) return false;
    if (!/^[{[]/.test(trimmed)) return false;
    try {
      JSON.parse(trimmed);
      return true;
    } catch {
      return false;
    }
  };

  const useLatestAsTemplate = async () => {
    setTemplating(true);
    setTemplateError(null);
    setError(null);
    setValueError(null);
    try {
      // Fan out across partitions: we don't know which one holds the freshest
      // record, so pull the tail-most message from each and keep the newest
      // by timestamp_ms (ties broken by offset).
      const probes: Promise<Message[]>[] = partitions.map((p) =>
        fetchMessages(cluster, topic, {
          partition: p.partition,
          from: "end",
          limit: 1,
        })
          .then((page) => page.messages)
          .catch(() => [] as Message[]),
      );
      const results = await Promise.all(probes);
      const candidates = results.flat();
      if (candidates.length === 0) {
        throw new Error("No messages in this topic yet");
      }
      candidates.sort((a, b) => {
        if (b.timestamp_ms !== a.timestamp_ms) return b.timestamp_ms - a.timestamp_ms;
        if (b.partition !== a.partition) return b.partition - a.partition;
        return b.offset - a.offset;
      });
      const latest = candidates[0];

      setKey(latest.key ?? "");
      const v = latest.value ?? "";
      if (looksLikeJSON(v)) {
        try {
          setValue(JSON.stringify(JSON.parse(v), null, 2));
          setValueFormat("json");
        } catch {
          setValue(v);
          setValueFormat("text");
        }
      } else {
        setValue(v);
        setValueFormat("text");
      }
      const hdrs = latest.headers
        ? Object.entries(latest.headers).map(([k, val]) => ({ k, v: val }))
        : [];
      setHeaders(hdrs);
    } catch (e) {
      setTemplateError((e as Error).message);
    } finally {
      setTemplating(false);
    }
  };

  const prettifyJSON = () => {
    try {
      setValue(JSON.stringify(JSON.parse(value), null, 2));
      setValueFormat("json");
      setValueError(null);
    } catch (e) {
      setValueError(`Value is not valid JSON: ${(e as Error).message}`);
    }
  };

  const runProduce = async (confirmProd = false) => {
    setError(null);
    setResult(null);
    if (valueFormat === "json" && value.trim()) {
      try {
        JSON.parse(value);
      } catch (e) {
        setValueError(`Value is not valid JSON: ${(e as Error).message}`);
        return;
      }
    }
    setValueError(null);
    setBusy(true);
    try {
      const hdrMap: Record<string, string> = {};
      for (const { k, v } of headers) {
        if (k.trim()) hdrMap[k.trim()] = v;
      }
      const partNum = partition === "auto" ? undefined : Number.parseInt(partition, 10);
      const res = await produceMessage(
        cluster,
        topic,
        {
          key,
          value,
          key_encoding: "text",
          value_encoding: "text",
          partition: partNum,
          headers: Object.keys(hdrMap).length > 0 ? hdrMap : undefined,
        },
        confirmProd,
      );
      setResult(res);
      await qc.invalidateQueries({ queryKey: messageKeys.topic(cluster, topic) });
      await qc.invalidateQueries({ queryKey: topicQueries.detail(cluster, topic).queryKey });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const submit = async () => {
    if (isProdCluster) {
      setConfirmProdOpen(true);
      return;
    }
    await runProduce();
  };

  const reset = () => {
    setKey("");
    setValue("");
    setHeaders([]);
    setPartition("auto");
    setValueFormat("text");
    setResult(null);
    setError(null);
    setValueError(null);
    setTemplateError(null);
  };

  const labelCls = "text-[11px] font-semibold uppercase tracking-wider text-muted";

  // RBAC reasons are load-bearing — surface via `aria-describedby`, not
  // hover-only `title=`.
  const rbacReasonId = "produce-rbac-disabled-reason";
  const templateReasonId = "produce-template-disabled-reason";

  let templateDisabledReason: string | null = null;
  if (!rbacAllowsConsume) {
    templateDisabledReason = "Forbidden by RBAC policy.";
  } else if (latestProbeQuery.isError) {
    templateDisabledReason = "Could not fetch latest message.";
  } else if (latestProbeQuery.data && !latestProbeAvailable) {
    templateDisabledReason = "No messages in this topic yet.";
  }
  const templateDisabled = templating || busy || templateDisabledReason !== null;

  return (
    <Card>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 className="text-sm font-semibold text-text">Produce message</h2>
        <Button
          variant="secondary"
          size="sm"
          leadingIcon={<History className="h-3.5 w-3.5" />}
          onClick={useLatestAsTemplate}
          loading={templating}
          disabled={templateDisabled}
          aria-describedby={templateDisabledReason ? templateReasonId : undefined}
        >
          {templating ? "Loading…" : "Use latest message"}
        </Button>
        {templateDisabledReason ? (
          <span id={templateReasonId} className="sr-only">
            {templateDisabledReason}
          </span>
        ) : null}
      </div>

      <div className="grid gap-4">
        <div className="flex flex-wrap items-center gap-3">
          <label htmlFor="produce-partition" className={labelCls}>
            Partition
          </label>
          <select
            id="produce-partition"
            value={partition}
            onChange={(e) => setPartition(e.target.value)}
            className="rounded-md border border-border bg-panel px-2 py-1 text-sm"
          >
            <option value="auto">auto (by key hash)</option>
            {partitions.map((p) => (
              <option key={p.partition} value={p.partition}>
                {p.partition}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label htmlFor="produce-key" className={`mb-1 block ${labelCls}`}>
            Key
          </label>
          <Input
            id="produce-key"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="(empty key)"
            className="font-mono"
          />
        </div>

        <div>
          <div className="mb-1 flex items-center justify-between">
            <label htmlFor="produce-value" className={labelCls}>
              Value
            </label>
            <div className="flex items-center gap-2 text-xs text-muted">
              <span
                className={
                  valueFormat === "json"
                    ? "rounded bg-subtle px-1.5 py-0.5 text-[10px] text-text"
                    : ""
                }
              >
                {valueFormat === "json" ? "JSON" : "text"}
              </span>
              <Button
                variant="secondary"
                size="sm"
                onClick={prettifyJSON}
                className="h-7 px-2 text-xs"
              >
                Format JSON
              </Button>
            </div>
          </div>
          <textarea
            id="produce-value"
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
              setValueFormat("text");
              setValueError(null);
            }}
            rows={6}
            placeholder='{"hello":"world"}'
            {...valueField.controlProps}
            className={
              "w-full rounded-md border bg-panel px-3 py-2 text-sm font-mono transition-colors " +
              (valueField.invalid ? "border-danger" : "border-border hover:border-border-hover")
            }
          />
          {valueField.message}
        </div>

        <div>
          <div className="mb-1 flex items-center justify-between">
            <span className={labelCls}>Headers</span>
            <Button
              variant="ghost"
              size="sm"
              leadingIcon={<Plus className="h-3.5 w-3.5" />}
              onClick={addHeader}
              className="h-7 px-2 text-xs"
            >
              Add header
            </Button>
          </div>
          {headers.length === 0 ? (
            <div className="text-xs text-subtle-text">no headers</div>
          ) : (
            <div className="grid gap-1.5">
              {headers.map((h, i) => (
                <div key={i} className="flex items-center gap-2">
                  <Input
                    aria-label={`Header ${i + 1} key`}
                    placeholder="key"
                    value={h.k}
                    onChange={(e) => updateHeader(i, "k", e.target.value)}
                    className="w-1/3 font-mono"
                  />
                  <Input
                    aria-label={`Header ${i + 1} value`}
                    placeholder="value"
                    value={h.v}
                    onChange={(e) => updateHeader(i, "v", e.target.value)}
                    className="flex-1 font-mono"
                  />
                  <IconButton
                    aria-label="Remove header"
                    size="sm"
                    variant="danger"
                    icon={<Trash2 className="h-3.5 w-3.5" />}
                    onClick={() => removeHeader(i)}
                  />
                </div>
              ))}
            </div>
          )}
        </div>

        {templateError && <StatusBox intent="danger">{templateError}</StatusBox>}
        {error && <StatusBox intent="danger">{error}</StatusBox>}
        {result && (
          <StatusBox intent="success">
            Produced · partition {result.partition} · offset {result.offset} ·{" "}
            <Timestamp value={result.timestamp_ms} className="text-xs text-success" />
          </StatusBox>
        )}

        <div className="flex items-center gap-2 pt-2">
          <Button
            variant="primary"
            size="sm"
            onClick={submit}
            loading={busy}
            disabled={busy || !rbacAllowsProduce}
            aria-describedby={!rbacAllowsProduce ? rbacReasonId : undefined}
          >
            {busy ? "Producing…" : "Produce"}
          </Button>
          <Button variant="ghost" size="sm" onClick={reset} disabled={busy}>
            Reset
          </Button>
          {!rbacAllowsProduce ? (
            <span id={rbacReasonId} className="sr-only">
              Forbidden by RBAC policy.
            </span>
          ) : null}
        </div>
      </div>
      <ConfirmDialog
        open={confirmProdOpen}
        onOpenChange={setConfirmProdOpen}
        title="Production cluster warning"
        description="You are about to produce a message to a production cluster. This can impact live systems."
        confirmLabel="Produce anyway"
        cancelLabel="Cancel"
        variant="danger"
        onConfirm={() => runProduce(true)}
      />
    </Card>
  );
}
