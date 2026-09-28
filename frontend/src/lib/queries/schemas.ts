import { queryOptions } from "@tanstack/react-query";
import { getSchemaVersion, listSubjects } from "../api";

/** Key prefixes for invalidating every cached version of a subject. */
export const schemaKeys = {
  subject: (cluster: string, subject: string) => ["schema", cluster, subject] as const,
};

// One key per resource: the Schemas page, the command palette and the
// topic's Schema tab share these entries (the tab passes version "latest").
export const schemaQueries = {
  subjects: (cluster: string) =>
    queryOptions({
      queryKey: ["schemas", cluster] as const,
      queryFn: () => listSubjects(cluster),
    }),
  version: (cluster: string, subject: string, version: string | number) =>
    queryOptions({
      queryKey: [...schemaKeys.subject(cluster, subject), version] as const,
      queryFn: () => getSchemaVersion(cluster, subject, version),
    }),
};
