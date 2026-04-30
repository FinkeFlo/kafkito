import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronDown, Settings } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { clsx } from "clsx";
import { useCluster, type ClusterListItem } from "@/lib/use-cluster";
import { StatusDot } from "./StatusDot";

function brokersHint(c: ClusterListItem | null): string {
  if (!c) return "";
  if (c.tls) return c.auth_type && c.auth_type !== "none" ? `${c.auth_type} · TLS` : "TLS";
  if (c.auth_type && c.auth_type !== "none") return c.auth_type;
  return "no auth";
}

export function ClusterPill({ className }: { className?: string }) {
  const { cluster, clusters, setCluster, isLoading } = useCluster();
  const [open, setOpen] = useState(false);
  const [activeIdx, setActiveIdx] = useState(0);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const itemsRef = useRef<Array<HTMLButtonElement | null>>([]);
  const popoverId = useId();

  const sorted = useMemo(() => {
    if (!clusters) return [];
    return [...clusters].sort((a, b) => a.name.localeCompare(b.name));
  }, [clusters]);

  const activeInfo = useMemo(
    () => sorted.find((c) => c.name === cluster) ?? null,
    [sorted, cluster],
  );

  const close = useCallback(() => {
    setOpen(false);
    triggerRef.current?.focus();
  }, []);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
      }
    };
    const onClick = (e: MouseEvent) => {
      const target = e.target as Node;
      if (
        popoverRef.current &&
        !popoverRef.current.contains(target) &&
        !triggerRef.current?.contains(target)
      ) {
        setOpen(false);
      }
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onClick);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onClick);
    };
  }, [open, close]);

  useLayoutEffect(() => {
    if (!open) return;
    const idx = sorted.findIndex((c) => c.name === cluster);
    const start = idx >= 0 ? idx : 0;
    setActiveIdx(start);
    requestAnimationFrame(() => {
      itemsRef.current[start]?.focus();
    });
  }, [open, sorted, cluster]);

  if (isLoading && !clusters) {
    return (
      <span
        aria-label="Loading clusters"
        className={clsx(
          "inline-flex h-8 items-center gap-2 rounded-full border border-border bg-panel px-3 text-xs text-muted",
          className,
        )}
      >
        <StatusDot reachable={false} />
        <span>loading…</span>
      </span>
    );
  }

  if (!clusters || clusters.length === 0) {
    return (
      <Link
        to="/settings/clusters"
        search={{ cluster: undefined }}
        className={clsx(
          "inline-flex h-8 items-center gap-2 rounded-full border border-border bg-panel px-3 text-xs text-muted transition-colors hover:bg-hover",
          className,
        )}
      >
        <Settings className="h-3.5 w-3.5" />
        <span>Connect cluster</span>
      </Link>
    );
  }

  const onListKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (sorted.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      const next = (activeIdx + 1) % sorted.length;
      setActiveIdx(next);
      itemsRef.current[next]?.focus();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      const next = (activeIdx - 1 + sorted.length) % sorted.length;
      setActiveIdx(next);
      itemsRef.current[next]?.focus();
    } else if (e.key === "Home") {
      e.preventDefault();
      setActiveIdx(0);
      itemsRef.current[0]?.focus();
    } else if (e.key === "End") {
      e.preventDefault();
      const last = sorted.length - 1;
      setActiveIdx(last);
      itemsRef.current[last]?.focus();
    }
  };

  const choose = (name: string) => {
    setCluster(name);
    close();
  };

  const reachable = activeInfo ? activeInfo.reachable : false;

  return (
    <div className={clsx("relative", className)}>
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={popoverId}
        aria-label={`Cluster: ${activeInfo?.name ?? "-"}`}
        className="inline-flex h-8 items-center gap-2 rounded-full border border-border bg-panel px-3 text-xs text-text transition-colors hover:bg-hover"
      >
        <StatusDot reachable={reachable} pulsing={reachable} />
        <span className="font-mono text-[12px] font-semibold">{activeInfo?.name ?? "-"}</span>
        {activeInfo && (
          <>
            <span className="text-muted" aria-hidden>·</span>
            <span className="text-text">{brokersHint(activeInfo)}</span>
          </>
        )}
        <ChevronDown className="h-3.5 w-3.5 text-muted" aria-hidden />
      </button>

      {open && (
        <div
          ref={popoverRef}
          id={popoverId}
          role="listbox"
          aria-label="Select cluster"
          tabIndex={-1}
          onKeyDown={onListKeyDown}
          className="absolute right-0 z-30 mt-2 w-80 rounded-xl border border-border bg-panel p-1.5 shadow-xl"
        >
          <div className="px-2 pb-1 pt-1 text-[10px] font-semibold uppercase tracking-wider text-muted">
            Switch cluster
          </div>
          <ul className="space-y-0.5">
            {sorted.map((c, i) => {
              const active = c.name === cluster;
              return (
                <li key={`${c.source}:${c.name}`}>
                  <button
                    ref={(el) => {
                      itemsRef.current[i] = el;
                    }}
                    type="button"
                    role="option"
                    aria-selected={active}
                    onClick={() => choose(c.name)}
                    className={clsx(
                      "flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors",
                      "hover:bg-hover",
                      active && "bg-accent-subtle",
                    )}
                  >
                    <StatusDot reachable={c.reachable} />
                    <span className="min-w-0 flex-1 truncate font-mono text-[12px] font-medium text-text">
                      {c.name}
                    </span>
                    <span className="text-[10px] text-muted">{brokersHint(c)}</span>
                    {active && <Check className="h-3.5 w-3.5 text-accent" aria-hidden />}
                  </button>
                </li>
              );
            })}
          </ul>
          <div className="mt-1.5 border-t border-border pt-1.5">
            <Link
              to="/settings/clusters"
              search={{ cluster: cluster ?? undefined }}
              onClick={close}
              className="flex items-center gap-2 rounded-md px-2.5 py-2 text-xs text-muted transition-colors hover:bg-hover hover:text-text"
            >
              <Settings className="h-3.5 w-3.5" aria-hidden />
              <span>Manage clusters…</span>
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}
