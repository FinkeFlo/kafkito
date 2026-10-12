import { Button } from "@/components/ui/Button";
import { Card } from "@/components/ui/Card";
import { ErrorState } from "@/components/ui/ErrorState";
import { Notice } from "@/components/ui/Notice";
import { Skeleton } from "@/components/ui/Skeleton";
import { PrivateClusterPasswordGate } from "@/features/clusters/PrivateClusterPasswordGate";
import type { CompareOptions } from "@/lib/message-compare";
import type { FingerprintOptions } from "@/lib/message-fingerprint";
import type { MessageRef } from "@/lib/message-reference";
import { useFormatters } from "@/lib/use-formatters";
import type { CompareView } from "./compare-search";
import { CompareResult } from "./CompareResult";
import { FingerprintCheck } from "./FingerprintCheck";
import { type ComparedMessage, useComparedMessage } from "./use-compared-message";

type Side = "A" | "B";

/**
 * Loads both messages and renders the comparison. Each side's cluster may be
 * a private cluster whose password this tab does not know yet; the gates ask
 * for it before anything is fetched.
 */
export function ComparePanel({
  refA,
  refB,
  options,
  view,
  onlyDiff,
  onUseOffset,
}: {
  refA: MessageRef;
  refB: MessageRef;
  options: CompareOptions;
  view: CompareView;
  onlyDiff?: boolean;
  onUseOffset: (side: Side, offset: number) => void;
}) {
  return (
    <PrivateClusterPasswordGate cluster={refA.cluster}>
      <PrivateClusterPasswordGate cluster={refB.cluster}>
        <LoadedPair
          refA={refA}
          refB={refB}
          options={options}
          view={view}
          onlyDiff={onlyDiff}
          onUseOffset={onUseOffset}
        />
      </PrivateClusterPasswordGate>
    </PrivateClusterPasswordGate>
  );
}

function LoadedPair({
  refA,
  refB,
  options,
  view,
  onlyDiff,
  onUseOffset,
}: {
  refA: MessageRef;
  refB: MessageRef;
  options: CompareOptions;
  view: CompareView;
  onlyDiff?: boolean;
  onUseOffset: (side: Side, offset: number) => void;
}) {
  const a = useComparedMessage(refA);
  const b = useComparedMessage(refB);
  if (a.status === "ready" && b.status === "ready") {
    return (
      <CompareResult
        refA={refA}
        refB={refB}
        a={a}
        b={b}
        options={options}
        view={view}
        onlyDiff={onlyDiff}
      />
    );
  }
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <SideStatus side="A" target={refA} state={a} onUseOffset={onUseOffset} />
      <SideStatus side="B" target={refB} state={b} onUseOffset={onUseOffset} />
    </div>
  );
}

/** Message A against a pasted fingerprint. */
export function FingerprintPanel({
  refA,
  fingerprint,
  options,
  onUseOffset,
}: {
  refA: MessageRef;
  fingerprint: string;
  options: FingerprintOptions;
  onUseOffset: (side: Side, offset: number) => void;
}) {
  return (
    <PrivateClusterPasswordGate cluster={refA.cluster}>
      <LoadedFingerprint
        refA={refA}
        fingerprint={fingerprint}
        options={options}
        onUseOffset={onUseOffset}
      />
    </PrivateClusterPasswordGate>
  );
}

function LoadedFingerprint({
  refA,
  fingerprint,
  options,
  onUseOffset,
}: {
  refA: MessageRef;
  fingerprint: string;
  options: FingerprintOptions;
  onUseOffset: (side: Side, offset: number) => void;
}) {
  const a = useComparedMessage(refA);
  if (a.status === "ready") {
    return <FingerprintCheck a={a} fingerprint={fingerprint} options={options} />;
  }
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      <SideStatus side="A" target={refA} state={a} onUseOffset={onUseOffset} />
    </div>
  );
}

function SideStatus({
  side,
  target,
  state,
  onUseOffset,
}: {
  side: Side;
  target: MessageRef;
  state: ComparedMessage;
  onUseOffset: (side: Side, offset: number) => void;
}) {
  const fmt = useFormatters();
  switch (state.status) {
    case "loading":
      return (
        <Card aria-busy="true" className="space-y-2">
          <span className="sr-only">Loading message {side}</span>
          <Skeleton height="h-4" width="w-1/3" />
          <Skeleton height="h-4" width="w-full" />
          <Skeleton height="h-24" width="w-full" />
        </Card>
      );
    case "error":
      // A refused read is a missing permission, not a failure to retry.
      if (state.error.message.startsWith("HTTP 403")) {
        return (
          <Notice intent="warning" title={`No access to message ${side}`}>
            Reading it needs the consume permission on topic{" "}
            <span className="font-mono">{target.topic}</span> of cluster{" "}
            <span className="font-mono">{target.cluster}</span>, granted by kafkito&apos;s RBAC and
            the broker&apos;s ACLs. Ask an administrator, or compare another message.
          </Notice>
        );
      }
      return (
        <ErrorState
          title={`Message ${side} could not be loaded`}
          detail={state.error.message}
          onRetry={state.retry}
        />
      );
    case "missing":
      return (
        <Notice
          intent="warning"
          title={
            <>
              No record at offset{" "}
              <span className="font-mono tabular-nums">{fmt.number(target.offset)}</span> in
              partition <span className="font-mono tabular-nums">{target.partition}</span> of{" "}
              <span className="font-mono">{target.topic}</span>
            </>
          }
          actions={
            state.next !== null ? (
              <Button
                size="sm"
                variant="secondary"
                onClick={() => onUseOffset(side, state.next as number)}
              >
                Use offset {fmt.number(state.next)}
              </Button>
            ) : undefined
          }
        >
          {state.next !== null
            ? `The offset may have been compacted, deleted or used by a transaction marker. The next record is at offset ${fmt.number(state.next)}.`
            : "The partition holds no record at or after this offset."}
        </Notice>
      );
    case "ready":
      return <Card className="text-sm text-muted">Message {side} is loaded.</Card>;
  }
}
