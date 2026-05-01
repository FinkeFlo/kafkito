import { createFileRoute, redirect } from "@tanstack/react-router";
import { getLastCluster } from "@/lib/last-cluster";

export const Route = createFileRoute("/")({
  beforeLoad: () => {
    const last = getLastCluster();
    if (last) {
      throw redirect({
        to: "/clusters/$cluster",
        params: { cluster: last },
      });
    }
    throw redirect({ to: "/clusters", search: { cluster: undefined } });
  },
});
