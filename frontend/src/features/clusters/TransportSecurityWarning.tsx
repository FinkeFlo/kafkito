import { Notice } from "@/components/ui/Notice";
import type { PrivateClusterAuth } from "@/lib/private-clusters";

function warningText(
  authType: PrivateClusterAuth["type"],
  tlsEnabled: boolean,
  tlsInsecure: boolean,
): string | null {
  if (!tlsEnabled) {
    return authType === "plain"
      ? "SASL/PLAIN without TLS sends the username and password in cleartext, and all data as well."
      : "Without TLS, all data travels unencrypted.";
  }
  if (tlsInsecure) {
    return "With Skip verify, the broker's certificate is not checked, so the connection can be intercepted.";
  }
  return null;
}

// Inline warning for the broker connection settings of a private cluster.
// It never blocks saving or testing; it only says what the settings expose.
export function TransportSecurityWarning({
  authType,
  tlsEnabled,
  tlsInsecure,
  className,
}: {
  authType: PrivateClusterAuth["type"];
  tlsEnabled: boolean;
  tlsInsecure: boolean;
  className?: string;
}) {
  const text = warningText(authType, tlsEnabled, tlsInsecure);
  if (!text) return null;
  return (
    <Notice intent="warning" className={className}>
      {text}
    </Notice>
  );
}
