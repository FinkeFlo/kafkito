import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters/$cluster/")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/clusters/$cluster/topics",
      params: { cluster: params.cluster },
    });
  },
});
