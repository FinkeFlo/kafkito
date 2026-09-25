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
 * Length budget for changelog copy, enforced by `changelog.test.ts`.
 *
 * The What's-new modal is a single scrolling panel: every extra line of
 * prose pushes the next release further out of view, so entries must read
 * as a scannable list, not as release prose. Keep to what a user needs to
 * recognize the change — the "why", the reproduction steps and the
 * implementation details belong in the PR, not here.
 *
 * The numbers are derived from the panel, not chosen freely: the `lg`
 * modal is `max-w-2xl` (672px), and after the panel padding and the badge
 * column roughly 560px remain for text. At `text-sm` that is ~80
 * characters per line, so the budget is one line for a title and two for
 * a description.
 *
 * A description is optional and should be omitted whenever the title
 * already says it — prefer a sharper title over a title plus a sentence
 * restating it.
 */
export const MAX_CHANGELOG_TITLE_LENGTH = 70;
export const MAX_CHANGELOG_DESCRIPTION_LENGTH = 160;

/**
 * Curated release notes, newest first. Add a new entry as part of the
 * release checklist BEFORE tagging; `version` must equal the normalized
 * runtime version (see lib/whats-new.ts `normalizeVersion`).
 *
 * Keep titles and descriptions within the length budget above.
 */
export const CHANGELOG: ChangelogEntry[] = [
  {
    version: "1.1.19",
    date: "2026-09-25",
    items: [
      {
        type: "fix",
        title: "Field-path suggestions for messages that are a JSON array",
        description:
          "A value like [{...},{...}] produced no suggestions and the misleading hint \"Sample isn't JSON\". Its fields are now offered under the $[*] prefix.",
      },
      {
        type: "fix",
        title: "Long dialogs no longer overflow the window",
        description:
          "A dialog taller than the browser window — such as What's new — ran past the top and bottom edges. Dialogs now fit the window and scroll inside.",
      },
      {
        type: "fix",
        title: "Honest hint when a sample is too large for suggestions",
        description:
          "Values above the 4 MB scan limit were reported as \"Sample isn't JSON\", which was untrue. The hint now names the size limit as the reason.",
      },
      {
        type: "fix",
        title: "Field-path suggestions list field names only",
        description:
          "The dropdown also showed a sample value and a distinct count per field, which could spill a huge value across the list. It now lists paths.",
      },
      {
        type: "fix",
        title: "Message list no longer claims an empty topic while loading",
        description:
          "\"No messages.\" appeared while the first page was still loading. The list now says it is loading until the result is actually known.",
      },
      {
        type: "fix",
        title: "Dropdowns and scrollbars follow the chosen theme",
        description:
          "With a pinned theme, native controls followed the operating system instead — a dark dropdown on the light canvas. They now match the app.",
      },
      {
        type: "fix",
        title: "Click-to-filter size limit stated in a comparable unit",
        description:
          "The limit read \"977 KiB\" next to a value size in MiB, so it took mental arithmetic to tell them apart. It is now a round 1 MiB.",
      },
    ],
  },
  {
    version: "1.1.18",
    date: "2026-09-25",
    items: [
      {
        type: "fix",
        title: "Search now finds matches anywhere in large messages",
        description:
          "Contains, JSONPath, XPath and JS search only scanned the first 64 KB of a value. They now scan the full record; only result previews stay capped.",
      },
      {
        type: "fix",
        title: "Click-to-filter works for large (truncated) messages",
        description:
          "Values over 64 KB fell back to plain text. A \"Load full value\" button now fetches the record first. Covers JSON up to 1 MB; not Schema Registry topics.",
      },
      {
        type: "fix",
        title: "Field-path suggestions no longer miss fields in large samples",
        description:
          "Sample messages were truncated at 64 KB, so fields could be missing from the suggestion list. Samples are now hydrated with their full value first.",
      },
      {
        type: "feature",
        title: "Substring matching and highlighting in the field-path suggestion list",
        description:
          "Typing \"pric\" now matches \"$.order.items[*].price\", with the hits highlighted. Several words match in any order, so \"order price\" finds it too.",
      },
      {
        type: "feature",
        title: "Field-path suggestions for XPath search",
        description:
          "XPath mode now has the same suggestion dropdown as JSONPath, built from sample messages — elements and attributes, with repeated siblings collapsed.",
      },
      {
        type: "fix",
        title: "Clicking an array value always searches every entry",
        description:
          "The prompt asking for one index or every entry is gone — clicking a value inside an array now always builds the items[*] wildcard form.",
      },
      {
        type: "fix",
        title: "Large XML values are now labeled correctly",
        description:
          "An XML value over 64 KB is only a truncated preview, which used to be labeled \"text\". Encoding detection is now truncation-tolerant for XML too.",
      },
      {
        type: "fix",
        title: "Long field names in the path suggestion list no longer overflow",
        description:
          "Long paths in the suggestion dropdown could overlap neighboring controls. They are now truncated with an ellipsis — hover to see the full path.",
      },
    ],
  },
  {
    version: "1.1.17",
    date: "2026-09-14",
    items: [
      {
        type: "fix",
        title: "Larger, gzip-compressed produce requests for the Replay dialog",
        description:
          "The produce request itself was capped at 4 MB, so replaying a large value could still fail. The limit is now 15 MB, and large payloads are gzipped.",
      },
      {
        type: "fix",
        title: "Clearer replay dialog behavior",
        description:
          "The destination-topic picker is now a styled dropdown instead of the browser autocomplete, and the wording after a successful replay is clearer.",
      },
      {
        type: "fix",
        title: "Warning when the newest messages may be missing from a page",
        description:
          "A very large record ahead of the newest page could make it come back incomplete. A warning now appears when that happens, so you know to retry.",
      },
    ],
  },
  {
    version: "1.1.16",
    date: "2026-09-14",
    items: [
      {
        type: "fix",
        title: "Clear error when a produced message is too large",
        description:
          "An oversized message failed with a generic \"upstream kafka error\". The limit is now explicitly 10 MB and the error states that size limit.",
      },
    ],
  },
  {
    version: "1.1.15",
    date: "2026-09-14",
    items: [
      {
        type: "fix",
        title: "Replay no longer silently truncates large values",
        description:
          "Replaying a message over 64 KB used to re-send only its truncated preview. Replay now fetches the full value first, or warns before you opt in.",
      },
      {
        type: "fix",
        title: "Bulk topic copy now skips (instead of truncating) oversized values",
        description:
          "Records whose value was truncated in the source list are now counted as skipped, instead of being written to the destination in shortened form.",
      },
    ],
  },
  {
    version: "1.1.14",
    date: "2026-09-14",
    items: [
      {
        type: "fix",
        title: "Replay/produce now reports Kafka ACL denials clearly",
        description:
          "Writing to a topic the cluster credential isn't authorized for now returns a clear 403 instead of a generic \"upstream kafka error\".",
      },
      {
        type: "feature",
        title: "Search private clusters by name or broker",
        description:
          "The Private clusters settings page now has the same filter box as Topics: narrow the list by cluster name or broker, with a live match counter.",
      },
    ],
  },
  {
    version: "1.1.13",
    date: "2026-08-12",
    items: [
      {
        type: "fix",
        title: "Config-restricted warning can now be dismissed",
        description:
          "The warning shown when topic config access is restricted can now be dismissed, so it no longer permanently occupies screen space.",
      },
    ],
  },
  {
    version: "1.1.12",
    date: "2026-08-12",
    items: [
      {
        type: "fix",
        title: "Download full value now works for private clusters",
        description:
          "The button returned HTTP 400 for private (browser-stored) clusters because the cluster header was missing. It is now sent correctly.",
      },
    ],
  },
  {
    version: "1.1.11",
    date: "2026-08-12",
    items: [
      {
        type: "feature",
        title: "Download full message value as a file",
        description:
          "Values truncated at 64 KB can now be downloaded in full from the expanded row. Content-Type is auto-detected; values over 15 MB are rejected.",
      },
    ],
  },
  {
    version: "1.1.10",
    date: "2026-08-12",
    items: [
      {
        type: "fix",
        title: "Large message values are now safely previewed",
        description:
          "Values larger than 64 KB are truncated before decoding. The row shows a \"preview\" badge and the expanded view notes the original size.",
      },
    ],
  },
  {
    version: "1.1.9",
    date: "2026-08-12",
    items: [
      {
        type: "fix",
        title: "Consume limit is now capped and config errors are cached",
        description:
          "Fetch limits above 500 are clamped instead of rejected, and topic config reads are cached so a missing ACL no longer causes repeated round-trips.",
      },
    ],
  },
  {
    version: "1.1.8",
    date: "2026-08-05",
    items: [
      {
        type: "fix",
        title: "Reset Offsets now respects the production-cluster confirmation",
        description:
          "The modal was missing the production flag, causing a 428 on production-marked clusters. It now shows the warning and sends the flag.",
      },
    ],
  },
  {
    version: "1.1.7",
    date: "2026-08-05",
    items: [
      {
        type: "feature",
        title: "Copy messages between topics and clusters",
        description:
          "Copy a topic's messages into another topic, on the same or another cluster, with an optional time range, limit and partition. Progress is shown live.",
      },
      {
        type: "feature",
        title: "Replay a single message to any topic",
        description:
          "Every message now has a Replay action that re-sends just that record to a topic you pick, so you can reproduce one case without copying a range.",
      },
      {
        type: "fix",
        title: "Private clusters can use a Schema Registry again",
        description:
          "Browser-stored cluster settings lost every multi-word field, so the Schema Registry was never contacted and the TLS and production flags had no effect.",
      },
    ],
  },
  {
    version: "1.1.6",
    date: "2026-07-27",
    items: [
      {
        type: "security",
        title: "Hardened ad-hoc cluster fingerprinting",
        description:
          "The connection cache key for private clusters is now derived with a keyed HMAC instead of a plain hash, removing an offline brute-force risk.",
      },
      {
        type: "security",
        title: "Bounded search query length",
        description:
          "JSONPath/XPath search queries are now capped in length to prevent overly complex expressions from consuming excessive resources.",
      },
    ],
  },
  {
    version: "1.1.5",
    date: "2026-07-27",
    items: [
      {
        type: "feature",
        title: "Inject Kafkito metadata headers on produce",
        description:
          "Produced messages now carry `X-Kafkito-Source`, plus `X-Kafkito-User` when a user identity is available, to improve traceability and auditing.",
      },
      {
        type: "fix",
        title: "Regression coverage for produce metadata headers",
        description:
          "Added backend tests ensuring metadata headers are injected consistently, custom headers are preserved, and spoofed Kafkito headers are overwritten.",
      },
    ],
  },
  {
    version: "1.1.4",
    date: "2026-07-20",
    items: [
      {
        type: "feature",
        title: "Mark clusters as production in cluster management",
        description:
          "Clusters can now be marked as Production in Manage Clusters. The marker is persisted and shown in cluster overviews.",
        screenshot: {
          src: "/whats-new/1.1.3-prod-flag-toggle.png",
          alt: "Add cluster form with Environment section and Mark as Production checkbox enabled",
        },
      },
      {
        type: "security",
        title: "Safety confirmation before producing to production clusters",
        description:
          "Producing to a production-marked cluster now requires explicit confirmation. You can cancel safely, or continue with Produce anyway.",
        screenshot: {
          src: "/whats-new/1.1.3-prod-produce-warning.png",
          alt: "Produce tab showing a production warning confirmation dialog with Cancel and Produce anyway actions",
        },
      },
    ],
  },
  {
    version: "1.1.3",
    date: "2026-07-19",
    items: [
      {
        type: "feature",
        title: "Show message-count labels directly on Timeline bars",
        description:
          "The Timeline chart now displays message-count labels on bars to make volume changes easier to read at a glance.",
      },
      {
        type: "fix",
        title: "Timeline reliability improvements",
        description:
          "Improved timeline-related end-to-end stability and supporting test data handling to reduce flaky behavior.",
      },
    ],
  },
  {
    version: "1.1.2",
    date: "2026-07-17",
    items: [
      {
        type: "feature",
        title: "Preview how many messages a range holds before loading",
        description:
          "The Messages view now shows an estimated message count for the selected time or offset range, with an optional per-partition breakdown.",
      },
      {
        type: "feature",
        title: "New Timeline tab: message volume over time",
        description:
          "Every topic now has a Timeline tab showing estimated message counts per hour or day, for the last 24 hours, 7 days, or 30 days.",
        screenshot: {
          src: "/whats-new/1.1.1-timeline.png",
          alt: "Timeline tab showing a bar chart of message counts per day over the last 30 days",
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
          "If the cluster's ACLs don't permit a group name, kafkito now says so directly and shows the allowed prefixes when your key can read ACLs.",
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
          "SSRF guards on private-cluster dials, the RBAC subject derived from the verified JWT principal, and schema-registry basic auth refused over plain HTTP.",
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
