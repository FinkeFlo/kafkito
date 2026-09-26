import { queryOptions } from "@tanstack/react-query";
import { fetchInfo } from "../api";

export const infoQueries = {
  info: () =>
    queryOptions({
      queryKey: ["info"] as const,
      queryFn: fetchInfo,
      staleTime: 5 * 60_000,
    }),
};
