import { queryOptions } from "@tanstack/react-query";
import { apiFetch } from "../../auth/api";
import type { CurrentUser, Me } from "../../auth/types";

/** Prefix of every auth query; invalidate it to refresh the session state. */
export const authKeys = { all: ["auth"] as const };

export const authQueries = {
  currentUser: () =>
    queryOptions({
      queryKey: [...authKeys.all, "currentUser"] as const,
      queryFn: async (): Promise<CurrentUser> => {
        const res = await apiFetch("/user-api/currentUser");
        if (!res.ok) throw new Error(`currentUser ${res.status}`);
        return (await res.json()) as CurrentUser;
      },
      staleTime: 10 * 60 * 1000,
      retry: false,
    }),
  me: () =>
    queryOptions({
      queryKey: [...authKeys.all, "me"] as const,
      queryFn: async (): Promise<Me> => {
        const res = await apiFetch("/api/v1/me");
        if (!res.ok) throw new Error(`me ${res.status}`);
        return (await res.json()) as Me;
      },
      staleTime: 5 * 60 * 1000,
      retry: false,
    }),
};
