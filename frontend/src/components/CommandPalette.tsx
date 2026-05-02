import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  fetchBrokers,
  fetchClusters,
  fetchGroups,
  fetchTopics,
  listSubjects,
  type BrokerInfo,
  type ClusterInfo,
  type GroupInfo,
  type Subject,
  type TopicInfo,
} from "../lib/api";
import { useCluster } from "../lib/use-cluster";
import { useFuzzy } from "../lib/fuzzy";
import {
  Boxes,
  FileJson,
  Home,
  Search,
  Server,
  Shield,
  UserCog,
  Users,
} from "lucide-react";

type ItemKind =
  | "nav"
  | "cluster"
  | "topic"
  | "group"
  | "broker"
  | "subject";

type Item =
  | { kind: "nav"; label: string; to: string; icon: React.ReactNode }
  | {
      kind: "cluster";
      label: string;
      cluster: string;
      reachable: boolean;
    }
  | {
      kind: "topic";
      label: string;
      cluster: string;
      topic: string;
    }
  | {
      kind: "group";
      label: string;
      cluster: string;
      group: string;
      state: string;
    }
  | {
      kind: "broker";
      label: string;
      cluster: string;
      brokerId: number;
      host: string;
      port: number;
    }
  | {
      kind: "subject";
      label: string;
      cluster: string;
      subject: string;
      latest: number;
      versions: number;
    };

// Module-level pub-sub used by `<Shell>`'s search button to open the
// palette without dispatching a synthetic `KeyboardEvent`. The palette
// component subscribes on mount; any caller can fire `openCommandPalette()`
// to flip it open.
type Listener = () => void;
const openListeners = new Set<Listener>();

export function openCommandPalette(): void {
  openListeners.forEach((listener) => listener());
}

export function subscribeCommandPalette(listener: Listener): () => void {
  openListeners.add(listener);
  return () => {
    openListeners.delete(listener);
  };
}

export function CommandPalette() {
  const { t } = useTranslation("palette");
  const tt = (k: string, opts?: Record<string, unknown>): string =>
    t(k as never, opts as never) as unknown as string;
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const navigate = useNavigate();

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const mac = navigator.platform.toLowerCase().includes("mac");
      if ((mac ? e.metaKey : e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((p) => !p);
      }
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", h);
    const unsubscribe = subscribeCommandPalette(() => setOpen(true));
    return () => {
      window.removeEventListener("keydown", h);
      unsubscribe();
    };
  }, []);

  useEffect(() => {
    if (!open) {
      setQ("");
      setSel(0);
    }
  }, [open]);

  const { cluster: activeCluster, clusters } = useCluster();
  const activeInfo = activeCluster
    ? clusters?.find((c) => c.name === activeCluster)
    : undefined;
  const hasSR = activeInfo ? activeInfo.schema_registry : undefined;

  const clustersQ = useQuery({
    queryKey: ["clusters"],
    queryFn: fetchClusters,
    enabled: open,
  });
  const topicsQ = useQuery({
    queryKey: ["topics", activeCluster],
    queryFn: () => fetchTopics(activeCluster!),
    enabled: open && !!activeCluster,
  });
  const groupsQ = useQuery({
    queryKey: ["groups", activeCluster],
    queryFn: () => fetchGroups(activeCluster!),
    enabled: open && !!activeCluster,
  });
  const brokersQ = useQuery({
    queryKey: ["brokers", activeCluster],
    queryFn: () => fetchBrokers(activeCluster!),
    enabled: open && !!activeCluster,
  });
  const subjectsQ = useQuery({
    queryKey: ["schemas", activeCluster],
    queryFn: () => listSubjects(activeCluster!),
    // Only probe the registry when we know the cluster has one; avoids a
    // guaranteed-404 flood for SR-less clusters like QAS.
    enabled: open && !!activeCluster && hasSR === true,
  });

  const allItems: Item[] = useMemo(() => {
    const base: Item[] = [
      { kind: "nav", label: tt("nav.home"), to: "/", icon: <Home className="h-3.5 w-3.5" /> },
    ];
    if (activeCluster) {
      const c = encodeURIComponent(activeCluster);
      base.push(
        { kind: "nav", label: tt("nav.topics"), to: `/clusters/${c}/topics`, icon: <Boxes className="h-3.5 w-3.5" /> },
        { kind: "nav", label: tt("nav.groups"), to: `/clusters/${c}/groups`, icon: <Users className="h-3.5 w-3.5" /> },
        { kind: "nav", label: tt("nav.schemas"), to: `/clusters/${c}/schemas`, icon: <FileJson className="h-3.5 w-3.5" /> },
        { kind: "nav", label: tt("nav.acls"), to: `/clusters/${c}/security/acls`, icon: <Shield className="h-3.5 w-3.5" /> },
        { kind: "nav", label: tt("nav.users"), to: `/clusters/${c}/security/users`, icon: <UserCog className="h-3.5 w-3.5" /> },
      );
    }
    const clusterItems: Item[] = (clustersQ.data ?? []).map((c: ClusterInfo) => ({
      kind: "cluster" as const,
      label: c.name,
      cluster: c.name,
      reachable: c.reachable,
    }));
    const topicItems: Item[] = activeCluster
      ? (topicsQ.data ?? []).map((t: TopicInfo) => ({
          kind: "topic" as const,
          label: t.name,
          cluster: activeCluster,
          topic: t.name,
        }))
      : [];
    const groupItems: Item[] = activeCluster
      ? (groupsQ.data ?? []).map((g: GroupInfo) => ({
          kind: "group" as const,
          label: g.group_id,
          cluster: activeCluster,
          group: g.group_id,
          state: g.state,
        }))
      : [];
    const brokerItems: Item[] = activeCluster
      ? (brokersQ.data ?? []).map((b: BrokerInfo) => ({
          kind: "broker" as const,
          label: `Broker ${b.node_id} · ${b.host}`,
          cluster: activeCluster,
          brokerId: b.node_id,
          host: b.host,
          port: b.port,
        }))
      : [];
    const subjectItems: Item[] = activeCluster
      ? (subjectsQ.data ?? []).map((s: Subject) => ({
          kind: "subject" as const,
          label: s.name,
          cluster: activeCluster,
          subject: s.name,
          latest: s.versions[s.versions.length - 1] ?? 1,
          versions: s.versions.length,
        }))
      : [];
    return [
      ...base,
      ...clusterItems,
      ...topicItems,
      ...groupItems,
      ...brokerItems,
      ...subjectItems,
    ];
  }, [
    activeCluster,
    clustersQ.data,
    topicsQ.data,
    groupsQ.data,
    brokersQ.data,
    subjectsQ.data,
    tt,
  ]);

  // Multi-token AND search via the project's canonical fuzzy helper (Fuse,
  // configured for Kafka identifiers in lib/fuzzy.ts).
  const fuzzy = useFuzzy(allItems, {
    keys: ["label"],
    query: q,
  });

  const items: Item[] = useMemo(() => {
    const ql = q.trim();
    if (!ql) return allItems.slice(0, 20);
    return fuzzy.results.slice(0, 50);
  }, [q, allItems, fuzzy.results]);

  // Render-order: keep buckets visually adjacent when there is a query.
  // When idle, the items array is already grouped by source bucket order.
  const renderItems: Item[] = useMemo(() => {
    if (!q.trim()) return items;
    const order: ItemKind[] = ["nav", "cluster", "topic", "group", "broker", "subject"];
    const grouped: Item[] = [];
    for (const kind of order) {
      for (const it of items) if (it.kind === kind) grouped.push(it);
    }
    return grouped;
  }, [items, q]);

  useEffect(() => {
    setSel(0);
  }, [q]);

  if (!open) return null;

  const pick = (it: Item) => {
    setOpen(false);
    if (it.kind === "nav") {
      // it.to is a runtime-built path like `/clusters/<encoded>/topics`; the
      // typed router accepts string targets at runtime, but the literal isn't
      // in the route union, so cast.
      navigate({ to: it.to as never });
    } else if (it.kind === "cluster") {
      navigate({
        to: "/clusters/$cluster/topics",
        params: { cluster: it.cluster },
      });
    } else if (it.kind === "topic") {
      navigate({
        to: "/clusters/$cluster/topics/$topic",
        params: { cluster: it.cluster, topic: it.topic },
      });
    } else if (it.kind === "group") {
      navigate({
        to: "/clusters/$cluster/groups/$group",
        params: { cluster: it.cluster, group: it.group },
      });
    } else if (it.kind === "broker") {
      navigate({
        to: "/clusters/$cluster/brokers/$id",
        params: { cluster: it.cluster, id: String(it.brokerId) },
      });
    } else {
      navigate({
        to: "/clusters/$cluster/schemas/$subject",
        params: { cluster: it.cluster, subject: it.subject },
      });
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-[var(--color-text)]/50 p-4 pt-[10vh]"
      onClick={() => setOpen(false)}
    >
      <div
        className="w-full max-w-xl overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-raised)] shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-[var(--color-border)] px-3 py-2">
          <Search className="h-4 w-4 text-[var(--color-text-subtle)]" />
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setSel((s) => Math.min(s + 1, renderItems.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setSel((s) => Math.max(s - 1, 0));
              } else if (e.key === "Enter") {
                e.preventDefault();
                const it = renderItems[sel];
                if (it) pick(it);
              }
            }}
            placeholder={tt("placeholder")}
            className="flex-1 bg-transparent text-sm"
          />
          <kbd className="rounded border border-[var(--color-border)] bg-[var(--color-surface-subtle)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-text-muted)]">
            {tt("actions.esc")}
          </kbd>
        </div>
        <div className="max-h-[60vh] overflow-y-auto py-1">
          {renderItems.length === 0 && (
            <div className="p-6 text-center text-sm text-[var(--color-text-muted)]">{tt("noResults")}</div>
          )}
          {renderItems.map((it, i) => (
            <button
              key={`${it.kind}-${it.label}-${i}`}
              onMouseEnter={() => setSel(i)}
              onClick={() => pick(it)}
              className={[
                "flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
                i === sel ? "bg-[var(--color-surface-subtle)]" : "hover:bg-[var(--color-surface-subtle)]",
              ].join(" ")}
            >
              <span className="w-16 shrink-0 text-[10px] font-semibold uppercase tracking-wider text-[var(--color-text-subtle)]">
                {tt(`category.${it.kind}`)}
              </span>
              {it.kind === "nav" && it.icon}
              {it.kind === "group" && <Users className="h-3.5 w-3.5 text-[var(--color-text-subtle)]" />}
              {it.kind === "broker" && <Server className="h-3.5 w-3.5 text-[var(--color-text-subtle)]" />}
              {it.kind === "subject" && <FileJson className="h-3.5 w-3.5 text-[var(--color-text-subtle)]" />}
              <span className="font-mono">{it.label}</span>
              {it.kind === "cluster" && !it.reachable && (
                <span className="ml-auto text-[10px] text-[var(--color-danger)]">{tt("cluster.unreachable")}</span>
              )}
              {it.kind === "topic" && (
                <span className="ml-auto text-[10px] text-[var(--color-text-subtle)]">{tt("topic.on", { cluster: it.cluster })}</span>
              )}
              {it.kind === "group" && (
                <span className="ml-auto text-[10px] text-[var(--color-text-subtle)]">{it.state}</span>
              )}
              {it.kind === "broker" && (
                <span className="ml-auto text-[10px] text-[var(--color-text-subtle)]">
                  {tt("broker.host", { host: it.host, port: it.port })}
                </span>
              )}
              {it.kind === "subject" && (
                <span className="ml-auto text-[10px] text-[var(--color-text-subtle)]">
                  {tt("subject.versions", { latest: it.latest, count: it.versions })}
                </span>
              )}
            </button>
          ))}
        </div>
        <div className="flex items-center justify-between border-t border-[var(--color-border)] bg-[var(--color-surface-subtle)] px-3 py-1.5 text-[10px] text-[var(--color-text-muted)]">
          <span>
            <kbd className="rounded bg-[var(--color-surface-raised)] px-1 font-mono">↑↓</kbd> {tt("footer.navigate")} ·{" "}
            <kbd className="rounded bg-[var(--color-surface-raised)] px-1 font-mono">↵</kbd> {tt("footer.open")}
          </span>
          <span>
            <kbd className="rounded bg-[var(--color-surface-raised)] px-1 font-mono">⌘K</kbd> /{" "}
            <kbd className="rounded bg-[var(--color-surface-raised)] px-1 font-mono">Ctrl-K</kbd>
          </span>
        </div>
      </div>
    </div>
  );
}
