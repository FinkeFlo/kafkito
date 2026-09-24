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
    version: "1.1.18",
    date: "2026-09-24",
    items: [
      {
        type: "fix",
        title: "Search now finds matches anywhere in large messages",
        description:
          "Contains, JSONPath, XPath and JS search all matched only against the first 64 KB of a value, so a message larger than that could silently look like it had no matches at all (or fail to parse for the path-based modes). Search now scans each record's full content; only the message previews in results still cap at 64 KB.",
      },
      {
        type: "fix",
        title: "Click-to-filter works for large (truncated) messages",
        description:
          "Clicking a value to build a JSONPath/XPath query silently fell back to plain text for any message over 64 KB, since the truncated preview usually isn't valid JSON. A \"Load full value to enable click-to-filter\" button now fetches the complete record on demand before rendering the interactive tree.",
      },
      {
        type: "fix",
        title: "Field-path suggestions no longer miss fields from large sample messages",
        description:
          "The JSONPath field-picker's suggestion list is built from a few sample messages, which were truncated the same way and so could omit or drop fields entirely for topics with large messages. Truncated samples are now hydrated with their full value before building the suggestion tree.",
      },
      {
        type: "feature",
        title: "Fuzzy matching and highlighting in the field-path suggestion list",
        description:
          "Typing a partial or misspelled field name (e.g. \"pric\" for \"$.order.items[*].price\") now finds matches anywhere in the path, with the matched characters highlighted — consistent with fuzzy search elsewhere in the app.",
      },
      {
        type: "fix",
        title: "Clicking an array value always searches every entry",
        description:
          "Clicking a value inside an array used to ask whether to match only that one index (e.g. items[1].sku) or every entry (items[*].sku). A fixed index rarely makes sense — array order and length vary between messages — so click-to-filter now always builds the items[*] (wildcard) form and skips the extra step.",
      },
      {
        type: "fix",
        title: "Large XML values are now labeled correctly",
        description:
          "Like JSON, an XML value larger than 64 KB is only ever a truncated preview, and truncation almost always breaks a value's structure — the encoding was falling back to \"text\" even for genuinely XML records. Encoding detection is now truncation-tolerant for XML too, so the badge and downstream tooling see \"xml\" instead.",
      },
      {
        type: "fix",
        title: "Long field names in the path suggestion list no longer overflow",
        description:
          "A deeply nested or long field name in the JSONPath suggestion dropdown could overflow past the edge of the list and overlap neighboring controls. Long paths are now truncated with an ellipsis (hover to see the full path).",
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
          "Replaying a recovered value close to the new 10 MB producer limit could still fail because the produce request itself was capped at 4 MB. The request size limit is now 15 MB, and large payloads are gzip-compressed before sending to reduce transfer size.",
      },
      {
        type: "fix",
        title: "Clearer replay dialog behavior",
        description:
          "The destination-topic picker now uses a larger, styled dropdown instead of the plain browser autocomplete. The success message after loading a truncated value now says \"loaded\" instead of \"recovered\", and the dialog's close button is labeled \"Close\" instead of \"Cancel\" once a replay has succeeded.",
      },
      {
        type: "fix",
        title: "Warning when the newest messages may be missing from a page",
        description:
          "Loading the newest page of a topic could silently return an incomplete page if a very large record ahead of it slowed down the fetch. A warning now appears when this happens, so you know to retry.",
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
          "Replaying or producing a message larger than the client's batch limit used to fail with a generic \"HTTP 502: upstream kafka error\". The limit is now explicitly set to 10 MB and an oversized message now returns a clear error stating the size limit instead of a vague gateway error.",
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
          "Messages over 64 KB are only ever held as a truncated preview in the message list. Replaying such a message used to reproduce just that 64 KB preview as if it were the whole record — a silent data-corruption bug. Replay now automatically fetches the full value first (up to the existing 15 MB raw-download limit) and sends it byte-for-byte; if that isn't possible, you're shown a clear warning and must explicitly opt in to replay the truncated preview instead.",
      },
      {
        type: "fix",
        title: "Bulk topic copy now skips (instead of truncating) oversized values",
        description:
          "For the same reason, bulk \"Copy topic\" jobs now skip records whose value was truncated in the source list, counting them as skipped rather than silently writing a partial value to the destination. Per-record full-value fetches were deliberately not added to the bulk path, to avoid reintroducing the memory/latency risk the 64 KB cap exists to prevent at scale.",
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
          "Replaying or producing a message to a topic the connected credential isn't authorized to write to used to fail with a generic \"HTTP 502: upstream kafka error\". It now returns a clear 403 explaining that the cluster credential's ACLs don't permit the write, instead of a vague gateway error.",
      },
      {
        type: "feature",
        title: "Search private clusters by name or broker",
        description:
          "The Private clusters settings page now has the same filter box as Topics: type to narrow the list by cluster name or broker address, with a live match counter.",
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
          "The topic-level warning shown when config access is restricted now includes a dismiss button, so it no longer permanently occupies screen space while you browse that topic.",
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
          "The Download full value button was returning HTTP 400 for private (browser-stored) clusters because the request was missing the required X-Kafkito-Cluster header. The header is now correctly injected, so downloads work for all cluster types.",
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
          "When a message value is too large to show in full (truncated at 64 KB), a Download full value button now appears in the expanded row. Clicking it fetches the raw bytes directly from Kafka and saves them as a file — the Content-Type is auto-detected (JSON, plain text, or binary), and values larger than 15 MB are rejected to keep downloads practical.",
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
          "Message values larger than 64 KB are truncated before decoding to avoid excessive memory use. The message row shows a 'preview' badge and the expanded view notes the original size, so it is always clear when you are seeing only the first 64 KB of a larger payload.",
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
          "Message fetch limits above 500 are silently clamped instead of rejected. Topic configuration reads are now cached (10 s for success, 60 s for permanent errors), so a missing DescribeConfigs ACL no longer causes repeated Kafka round-trips on every poll. The topic layout shows a notice when config access is restricted.",
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
          "The Reset Offsets modal was missing the production confirmation flag, causing a 428 error on production-marked clusters. The modal now surfaces the production warning and passes the flag correctly for both the preview and commit steps.",
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
          "Copy a topic's messages into another topic — on the same cluster or across clusters — with an optional time range, message limit, single source partition, and the option to keep each message on its original partition number. Progress is shown live while the copy runs, and the destination topic has to exist beforehand. Messages whose original bytes are not available (decoded through the Schema Registry, or redacted by data masking) are left out and counted as skipped rather than written in a changed form.",
      },
      {
        type: "feature",
        title: "Replay a single message to any topic",
        description:
          "Every message now has a Replay action that re-sends just that record to a topic you pick, on any cluster, so you can reproduce one case without copying a whole range.",
      },
      {
        type: "fix",
        title: "Private clusters can use a Schema Registry again",
        description:
          "Browser-stored cluster settings lost every multi-word field on the way to the server, so a private cluster's Schema Registry was never contacted, \"skip TLS verification\" had no effect, and clusters marked as production skipped the production confirmation prompt.",
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
          "The cache key used to reuse connections for private (browser-stored) clusters is now derived with a keyed HMAC instead of a plain hash, removing a theoretical offline brute-force risk if the key ever leaked (e.g. via logs).",
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
          "Messages produced via Kafkito now include `X-Kafkito-Source: true` automatically. When a user identity is available, `X-Kafkito-User` is also attached to improve traceability and auditing.",
      },
      {
        type: "fix",
        title: "Regression coverage for produce metadata headers",
        description:
          "Added targeted backend tests to ensure metadata headers are injected consistently, custom headers are preserved, and spoofed Kafkito metadata headers are overwritten.",
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
          "Clusters can now be marked as Production in Manage Clusters. The marker is persisted and shown in cluster overviews so operators can clearly identify high-impact environments.",
        screenshot: {
          src: "/whats-new/1.1.3-prod-flag-toggle.png",
          alt: "Add cluster form with Environment section and Mark as Production checkbox enabled",
        },
      },
      {
        type: "security",
        title: "Safety confirmation before producing to production clusters",
        description:
          "Producing to a production-marked cluster now requires explicit confirmation. You can cancel safely, or continue with Produce anyway when intended.",
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
          "The Messages view now shows an estimated message count for the range you've selected (time or offset), so you know how much you're about to page through. Open the optional per-partition breakdown to see how those messages are spread across partitions, and keep using \"load more\" to walk through the range.",
      },
      {
        type: "feature",
        title: "New Timeline tab: message volume over time",
        description:
          "Every topic now has a Timeline tab showing an estimated message count per time slot (hourly or daily) for the last 24 hours, 7 days, or 30 days — a quick way to spot trends, spikes, or quiet periods without picking a custom range. For fully custom time ranges, the range picker on the Messages tab is still the way to go.",
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
