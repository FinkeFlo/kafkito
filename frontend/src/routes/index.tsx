import { createFileRoute, redirect } from "@tanstack/react-router";
import { getLastCluster } from "@/lib/last-cluster";

export const Route = createFileRoute("/")({
  beforeLoad: () => {
    const last = getLastCluster();
    if (last) {
      // TODO(task4): /clusters/$cluster is not yet a typed route — Task 4 adds it.
      // Cast keeps the redirect target stable across the refactor.
      throw redirect({
        to: "/clusters/$cluster" as never,
        params: { cluster: last } as never,
      });
    }
    throw redirect({ to: "/clusters", search: { cluster: undefined } });
  },
});
