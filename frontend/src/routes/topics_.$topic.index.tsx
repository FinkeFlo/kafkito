import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/topics_/$topic/")({
  beforeLoad: ({ params, search }) => {
    throw redirect({
      to: "/topics/$topic/overview",
      params: { topic: params.topic },
      search: { cluster: (search as { cluster?: string }).cluster ?? "" },
    });
  },
});
