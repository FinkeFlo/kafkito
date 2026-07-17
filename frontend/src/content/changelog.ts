export type ChangelogItemType = "feature" | "fix" | "security";

export interface ChangelogItem {
  type: ChangelogItemType;
  title: string;
  description?: string;
  screenshot?: {
    /** Public asset URL, e.g. "/whats-new/1.1.0-range-count.png". */
    src: string;
    alt: string;
  };
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
          "The Messages toolbar now shows an approximate message count for the selected time range, so you can quickly check how many messages were sent to the topic in that period.",
        screenshot: {
          src: "/whats-new/1.1.0-range-count-preview.png",
          alt: "Messages toolbar with range picker and approximate message count preview",
        },
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
