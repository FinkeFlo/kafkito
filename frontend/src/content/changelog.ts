export type ChangelogItemType = "feature" | "fix" | "security";

export interface ChangelogItem {
  type: ChangelogItemType;
  title: string;
  description?: string;
}

export interface ChangelogEntry {
  /** Normalized version key, e.g. "0.0.0-rc17" (no leading v, no -btp/-local/-dev). */
  version: string;
  /** ISO date "YYYY-MM-DD". */
  date: string;
  items: ChangelogItem[];
}

/**
 * Curated release notes, newest first. Add a new entry as part of the
 * release checklist BEFORE tagging; `version` must equal the normalized
 * runtime version (see lib/whats-new.ts `normalizeVersion`).
 */
export const CHANGELOG: ChangelogEntry[] = [
  {
    version: "1.1.0",
    date: "2026-07-17",
    items: [
      {
        type: "feature",
        title: "Preview how many messages a range holds before loading",
        description:
          "The Messages view now shows an approximate message count for the range you've selected (time or offset), so you know roughly how much you're about to page through. Open the optional per-partition breakdown to see how those messages are spread across partitions, and keep using \"load more\" to walk through the range.",
      },
    ],
  },
  {
    version: "1.0.1",
    date: "2026-07-09",
    items: [
      {
        type: "fix",
        title: "Clearer errors when a consumer-group name isn't allowed",
        description:
          "If the cluster's ACLs don't permit a group name, kafkito now says so directly instead of a generic gateway error, and shows the allowed group prefixes when your key can read ACLs.",
      },
    ],
  },
  {
    version: "1.0.0",
    date: "2026-07-09",
    items: [
      {
        type: "feature",
        title: "kafkito 1.0.0 — first stable release",
        description:
          "kafkito is now used in production. This is the first stable release; versions from here follow semantic versioning.",
      },
      {
        type: "feature",
        title: "Create consumer groups bound to a topic",
        description:
          "Pre-create a consumer group on a topic with a chosen start position (earliest, latest, timestamp, or a specific offset).",
      },
      {
        type: "security",
        title: "Backend security hardening",
        description:
          "SSRF guards on private-cluster broker and schema-registry dials, RBAC subject derived from the verified JWT principal, and schema-registry basic auth refused over plaintext HTTP.",
      },
      {
        type: "fix",
        title: "Frontend correctness fixes",
        description:
          "Message de-duplication, load-more paging on filter changes, and consumer-group polling cleanup.",
      },
    ],
  },
];
