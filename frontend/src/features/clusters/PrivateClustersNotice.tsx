import { Notice } from "@/components/ui/Notice";
import type { PrivateClusterAccess } from "@/lib/use-private-cluster-access";

/**
 * Explains why private-cluster actions are unavailable. Disabled controls
 * point their `aria-describedby` at `id`, which names the reason only.
 */
export function PrivateClustersNotice({
  id,
  mode,
  className,
}: {
  id: string;
  mode: PrivateClusterAccess["mode"];
  className?: string;
}) {
  return (
    <Notice intent="warning" className={className}>
      <span id={id}>
        {mode === "role"
          ? "Your role does not allow private clusters."
          : "Private clusters are disabled on this server."}
      </span>{" "}
      Clusters saved in this browser are kept, and you can still edit, export or delete them.
    </Notice>
  );
}
