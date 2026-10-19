import { useQuery } from "@tanstack/react-query";
import { Camera } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import type { Message } from "@/lib/api";
import {
  downloadBlob,
  renderSnapshotPng,
  snapshotFilename,
  snapshotModel,
} from "@/lib/audit-snapshot";
import { prettyValue } from "@/lib/format";
import { DEFAULT_COMPARE_OPTIONS, messageContent } from "@/lib/message-compare";
import { messageFingerprint } from "@/lib/message-fingerprint";
import { authQueries } from "@/lib/queries/auth";
import { useCluster } from "@/lib/use-cluster";
import { useFormatters } from "@/lib/use-formatters";
import { useTimeZone } from "@/lib/use-timezone";

/**
 * Downloads a PNG of one message for test evidence: capture time, the user
 * from the token, cluster name, topic, partition, offset, record time, key,
 * fingerprint and value. It holds only what the row shows; masked parts stay
 * masked and a truncated value stays a preview.
 */
export function AuditSnapshotAction({
  m,
  cluster,
  topic,
}: {
  m: Message;
  cluster: string;
  topic: string;
}) {
  const fmt = useFormatters();
  const [zone] = useTimeZone();
  const { clusters } = useCluster();
  const me = useQuery(authQueries.me());
  const [busy, setBusy] = useState(false);

  const capture = async (e: React.MouseEvent) => {
    e.stopPropagation();
    setBusy(true);
    try {
      const capturedAt = new Date();
      const fp = await messageFingerprint(messageContent(m), {
        ...DEFAULT_COMPARE_OPTIONS,
        semantic: false,
        ignoreKafkitoHeaders: false,
      });
      const notes: string[] = [];
      if (m.masked) notes.push("Value modified by a data_masking rule.");
      if (m.key_masked) notes.push("Key modified by a data_masking rule.");
      if (m.value_truncated) {
        notes.push(
          `Value is a 64 KB preview; full size ${m.value_size_bytes ? fmt.bytes(m.value_size_bytes) : "unknown"}.`,
        );
      }
      const value =
        m.value_encoding === "null"
          ? "(null)"
          : m.value_encoding === "empty"
            ? "(empty)"
            : prettyValue(m.value ?? "", m.value_encoding);
      const model = snapshotModel({
        capturedAt,
        zone,
        user: me.data && !me.data.anonymous ? me.data.user || null : null,
        email: me.data && !me.data.anonymous ? me.data.email || null : null,
        cluster,
        isProd: clusters?.find((c) => c.name === cluster)?.is_prod ?? false,
        topic,
        partition: fmt.number(m.partition),
        offset: fmt.number(m.offset),
        timestampMs: m.timestamp_ms,
        key: m.key ?? null,
        fingerprint: fp.ok ? fp.fingerprint : `— (${fp.reason})`,
        notes,
        valueLabel: `value · ${m.value_encoding}`,
        value,
      });
      const png = await renderSnapshotPng(model);
      downloadBlob(png, snapshotFilename(cluster, topic, m.partition, m.offset, capturedAt));
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <button
      type="button"
      onClick={capture}
      disabled={busy}
      className="inline-flex items-center gap-1 rounded border border-border px-2 py-1 text-[11px] hover:border-border-hover disabled:opacity-50"
      title="Download a PNG with capture time, user, cluster, topic, offset and value"
    >
      <Camera aria-hidden="true" className="h-4 w-4" />
      {busy ? "Capturing…" : "Audit snapshot"}
    </button>
  );
}
