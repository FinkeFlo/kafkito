import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/clusters/$cluster/security/")({
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/clusters/$cluster/security/acls",
      params: { cluster: params.cluster },
    });
  },
});
