import { queryOptions } from "@tanstack/react-query";
import { getSchemaVersion, listSubjects } from "../api";

// The subject list and the latest version are cached under two keys each:
// the Schemas page and command palette use `schemas`/`schema`, the topic's
// Schema tab `subjects`/`schema-version`. Kept apart so caching stays as it
// was; merging them is a separate change.
export const schemaQueries = {
  subjects: (cluster: string) =>
    queryOptions({
      queryKey: ["schemas", cluster] as const,
      queryFn: () => listSubjects(cluster),
    }),
  topicSubjects: (cluster: string) =>
    queryOptions({
      queryKey: ["subjects", cluster] as const,
      queryFn: () => listSubjects(cluster),
    }),
  version: (cluster: string, subject: string, version: string | number) =>
    queryOptions({
      queryKey: ["schema", cluster, subject, version] as const,
      queryFn: () => getSchemaVersion(cluster, subject, version),
    }),
  latestVersion: (cluster: string, subject: string) =>
    queryOptions({
      queryKey: ["schema-version", cluster, subject, "latest"] as const,
      queryFn: () => getSchemaVersion(cluster, subject, "latest"),
    }),
};
