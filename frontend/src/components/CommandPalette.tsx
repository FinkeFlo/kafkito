import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  fetchClusters,
  fetchTopics,
  type ClusterInfo,
  type TopicInfo,
} from "../lib/api";
import { useCluster } from "../lib/use-cluster";
import { Boxes, FileJson, Home, Search, Shield, Users } from "lucide-react";

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

  const { cluster: activeCluster } = useCluster();
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

  const items: Item[] = useMemo(() => {
    const base: Item[] = [
      { kind: "nav", label: tt("nav.home"), to: "/", icon: <Home className="h-3.5 w-3.5" /> },
    ];
    if (activeCluster) {
      const c = encodeURIComponent(activeCluster);
      base.push(
        { kind: "nav", label: tt("nav.topics"), to: `/clusters/${c}/topics`, icon: <Boxes className="h-3.5 w-3.5" /> },
        { kind: "nav", label: tt("nav.groups"), to: `/clusters/${c}/groups`, icon: <Users className="h-3.5 w-3.5" /> },
        { kind: "nav", label: tt("nav.schemas"), to: `/clusters/${c}/schemas`, icon: <FileJson className="h-3.5 w-3.5" /> },
        { kind: "nav", label: tt("nav.acls"), to: `/clusters/${c}/acls`, icon: <Shield className="h-3.5 w-3.5" /> },
        { kind: "nav", label: tt("nav.users"), to: `/clusters/${c}/security/users`, icon: <Users className="h-3.5 w-3.5" /> },
      );
    }
    const clusters: Item[] = (clustersQ.data ?? []).map((c: ClusterInfo) => ({
      kind: "cluster" as const,
      label: c.name,
      cluster: c.name,
      reachable: c.reachable,
    }));
    const topics: Item[] = (topicsQ.data ?? []).map((t: TopicInfo) => ({
      kind: "topic" as const,
      label: t.name,
      cluster: activeCluster!,
      topic: t.name,
    }));
    const all = [...base, ...clusters, ...topics];
    const ql = q.trim().toLowerCase();
    if (!ql) return all.slice(0, 20);
    return all.filter((it) => it.label.toLowerCase().includes(ql)).slice(0, 30);
  }, [q, clustersQ.data, topicsQ.data, activeCluster]);

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
    } else {
      navigate({
        to: "/clusters/$cluster/topics/$topic",
        params: { cluster: it.cluster, topic: it.topic },
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
                setSel((s) => Math.min(s + 1, items.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setSel((s) => Math.max(s - 1, 0));
              } else if (e.key === "Enter") {
                e.preventDefault();
                const it = items[sel];
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
          {items.length === 0 && (
            <div className="p-6 text-center text-sm text-[var(--color-text-muted)]">{tt("noResults")}</div>
          )}
          {items.map((it, i) => (
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
              <span className="font-mono">{it.label}</span>
              {it.kind === "cluster" && !it.reachable && (
                <span className="ml-auto text-[10px] text-[var(--color-danger)]">{tt("cluster.unreachable")}</span>
              )}
              {it.kind === "topic" && (
                <span className="ml-auto text-[10px] text-[var(--color-text-subtle)]">{tt("topic.on", { cluster: it.cluster })}</span>
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
