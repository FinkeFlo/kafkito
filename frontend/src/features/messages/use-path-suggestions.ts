import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { SearchMode } from "@/lib/api";
import { isTooLargeToScan, type HydratableEncoding } from "@/lib/hydrate-sample";
import { buildPathTree } from "@/lib/path-tree";
import { buildXmlPathTree, looksLikeXml } from "@/lib/xml-path-tree";
import { messageQueries } from "@/lib/queries/messages";

/**
 * Field-path suggestion trees for the structured (JSONPath / XPath) search
 * modes, built from a sample of the topic.
 */
export function usePathSuggestions(
  cluster: string,
  topic: string,
  mode: SearchMode,
  searchOpen: boolean,
) {
  // Sample query (lazy, only when a structured — JSONPath or XPath — search
  // is open). Field-path suggestions need each sample message's full
  // structure, but the sample endpoint returns the same 64 KB-truncated
  // preview as the message list — silently starving PathSense of any field
  // that only appears past the truncation boundary (or dropping the message
  // outright, since truncated JSON/XML usually fails to parse).
  // hydrateTruncatedSampleMessages fetches the full raw value for any
  // truncated sample, falling back to the truncated preview on failure.
  //
  // Keyed by encoding, not by mode, so the two structured modes share a
  // cache entry whenever they hydrate the same thing. Hydration only fetches
  // values the active tree can parse: pulling up to MAX_HYDRATE_VALUE_BYTES
  // per sample for the builder that will discard them is pure waste.
  const sampleEncoding: HydratableEncoding = mode === "xpath" ? "xml" : "json";
  const sampleQuery = useQuery({
    ...messageQueries.sample(cluster, topic, sampleEncoding),
    enabled: searchOpen && (mode === "jsonpath" || mode === "xpath"),
  });

  const pathTree = useMemo(() => {
    const msgs = sampleQuery.data?.messages ?? [];
    const parsed: unknown[] = msgs
      .map((m) => {
        try {
          return JSON.parse(m.value ?? "");
        } catch {
          return null;
        }
      })
      // Arrays are kept: a record whose whole value is an array of rows is a
      // normal payload shape, and buildPathTree indexes it under `$[*]`.
      // Scalars carry no field paths and are dropped by the builder itself.
      .filter((v): v is object => v !== null && typeof v === "object");
    return buildPathTree(parsed);
  }, [sampleQuery.data]);

  // A sample above the hydration cap never gets its full value, so the tree
  // is built from a 64 KB fragment that is cut mid-structure and therefore
  // doesn't parse. Reporting that as "isn't JSON" is simply untrue — the
  // value is valid, it is just too large to scan for field names.
  const sampleTooLargeToScan = useMemo(
    () => (sampleQuery.data?.messages ?? []).some(isTooLargeToScan),
    [sampleQuery.data],
  );

  // XPath's suggestion tree is built from the same (already hydrated)
  // samples, parsed with the browser's DOMParser rather than JSON.parse.
  const xmlPathTree = useMemo(() => {
    const values = (sampleQuery.data?.messages ?? [])
      .map((m) => m.value ?? "")
      .filter(looksLikeXml);
    return buildXmlPathTree(values);
  }, [sampleQuery.data]);

  return { pathTree, xmlPathTree, sampleTooLargeToScan };
}

export type PathSuggestions = ReturnType<typeof usePathSuggestions>;
