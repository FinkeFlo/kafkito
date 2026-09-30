import { Notice } from "@/components/ui/Notice";
import type { BrokerIssue, ClusterInfo } from "@/lib/api";

// What the settings form shows after "Test connection": the probe result
// (reachable or not, with the brokers that failed), or the request error.
export type TestOutcome =
  | { kind: "probed"; info: ClusterInfo }
  | { kind: "error"; message: string };

// Dial errors on a cold broker DNS cache are often transient; say so.
function coldDNSHint(msg: string): string {
  const m = msg.toLowerCase();
  if (m.includes("i/o timeout") || m.includes("dial")) {
    return " — first probe is slow on cold broker DNS; cluster connections cache for ~15min after first contact, so a retry usually succeeds in <1s.";
  }
  return "";
}

function hostPort(host: string, port: number): string {
  return host.includes(":") ? `[${host}]:${port}` : `${host}:${port}`;
}

function issueText(issue: BrokerIssue): string {
  const where = `Broker ${issue.node_id} advertises ${hostPort(issue.host, issue.port)}`;
  if (issue.reason === "blocked") {
    return `${where}, which is not allowed for private clusters (it resolves to a loopback, link-local, multicast or unspecified address).`;
  }
  return `${where}, which did not answer: ${issue.error}`;
}

// The probe checks a bounded number of advertised brokers; name the rest.
function SkippedNote({ count }: { count: number | undefined }) {
  if (!count || count <= 0) return null;
  return (
    <p className="mt-1">
      {count === 1 ? "1 more broker was not checked." : `${count} more brokers were not checked.`}
    </p>
  );
}

export function TestConnectionResult({
  outcome,
  className,
}: {
  outcome: TestOutcome;
  className?: string;
}) {
  if (outcome.kind === "error") {
    return (
      <Notice intent="danger" className={className}>
        {`Error: ${outcome.message}${coldDNSHint(outcome.message)}`}
      </Notice>
    );
  }
  const { info } = outcome;
  if (info.reachable) {
    return (
      <Notice intent="success" className={className}>
        <p>{`OK — reachable (${info.auth_type}, TLS: ${info.tls ? "yes" : "no"})`}</p>
        <SkippedNote count={info.brokers_skipped} />
      </Notice>
    );
  }
  const issues = info.broker_issues ?? [];
  if (issues.length > 0) {
    return (
      <Notice intent="danger" className={className}>
        <p>
          The seed broker answered, but not every broker the cluster advertises can be reached.
          kafkito connects to the advertised addresses (advertised.listeners), not to the seed.
        </p>
        <ul aria-label="Broker issues" className="mt-1 list-disc pl-5">
          {issues.map((issue) => (
            <li key={issue.node_id}>{issueText(issue)}</li>
          ))}
        </ul>
        <SkippedNote count={info.brokers_skipped} />
      </Notice>
    );
  }
  const err = info.error ?? "unknown error";
  return (
    <Notice intent="danger" className={className}>
      {`Unreachable: ${err}${coldDNSHint(err)}`}
    </Notice>
  );
}
