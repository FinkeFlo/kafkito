import { Card } from "@/components/ui/Card";
import { MonoId } from "@/components/ui/MonoId";
import { Notice } from "@/components/ui/Notice";
import { Skeleton } from "@/components/ui/Skeleton";
import type { FingerprintOptions } from "@/lib/message-fingerprint";
import type { ComparedMessage } from "./use-compared-message";
import { useFingerprint } from "./use-fingerprint";
import { VerdictBadge } from "./VerdictBadge";

type Ready = Extract<ComparedMessage, { status: "ready" }>;

/** The settings a fingerprint was taken with, in words. */
export function describeFingerprintOptions(o: FingerprintOptions): string {
  const parts = [o.semantic ? "semantic" : "exact"];
  if (o.semantic && o.ignoreArrayOrder) parts.push("any array order");
  if (o.semantic && o.ignoreChildOrder) parts.push("any element order");
  parts.push(o.ignoreKafkitoHeaders ? "X-Kafkito-* headers ignored" : "all headers");
  return parts.join(", ");
}

/**
 * Side B is a pasted fingerprint: message A is fingerprinted with the same
 * settings and the two digests compared. Says whether they match, not where
 * they differ.
 */
export function FingerprintCheck({
  a,
  fingerprint,
  options,
}: {
  a: Ready;
  fingerprint: string;
  options: FingerprintOptions;
}) {
  const fp = useFingerprint(a.content, options);
  if (!fp) {
    return (
      <Card className="space-y-2">
        <Skeleton height="h-5" width="w-40" />
        <Skeleton height="h-4" width="w-full" />
      </Card>
    );
  }
  if (!fp.ok) {
    return (
      <Notice intent="warning" title="Message A cannot be fingerprinted">
        {fp.reason}
      </Notice>
    );
  }
  const match = fp.fingerprint === fingerprint.trim().toLowerCase();
  return (
    <Card data-testid="compare-summary" className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-sm font-semibold text-text">Result</h2>
        <VerdictBadge
          verdict={match ? (options.semantic ? "equivalent" : "identical") : "different"}
        />
      </div>
      <p className="text-sm text-text">
        {match
          ? "Message A has the pasted fingerprint."
          : "Message A does not have the pasted fingerprint."}{" "}
        Both were taken {describeFingerprintOptions(options)}. A fingerprint says whether two
        messages match, not where they differ.
      </p>
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
        <dt className="text-muted">Message A</dt>
        <dd className="min-w-0">
          <MonoId value={fp.fingerprint} tailChars={12} data-testid="fingerprint" />
        </dd>
        <dt className="text-muted">Pasted</dt>
        <dd className="min-w-0">
          <MonoId value={fingerprint.trim().toLowerCase()} tailChars={12} />
        </dd>
      </dl>
    </Card>
  );
}
