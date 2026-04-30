import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";

export const Route = createFileRoute("/settings/")({
  component: SettingsIndexPage,
});

function SettingsIndexPage() {
  return <DefaultsCard />;
}

function DefaultsCard() {
  const [tz, setTz] = useLocalStorage("kafkito.timezone", "UTC");
  const [buffer, setBuffer] = useLocalStorage("kafkito.tailBuffer", "10000");
  const [confirm, setConfirm] = useLocalStorage(
    "kafkito.confirmDestructive",
    "always",
  );

  return (
    <div className="rounded-xl border border-border bg-panel p-4">
      <div className="text-sm font-semibold">Defaults</div>
      <div className="mt-4 grid grid-cols-1 gap-y-4 sm:grid-cols-[200px_1fr]">
        <div className="text-sm text-muted">Default timezone</div>
        <select
          value={tz}
          onChange={(e) => setTz(e.target.value)}
          className="h-8 w-40 rounded-md border border-border bg-panel px-2 font-mono text-xs"
        >
          <option>UTC</option>
          <option>local</option>
        </select>

        <div className="text-sm text-muted">Tail buffer</div>
        <input
          type="number"
          min={100}
          value={buffer}
          onChange={(e) => setBuffer(e.target.value)}
          className="h-8 w-40 rounded-md border border-border bg-panel px-2 font-mono text-xs"
        />

        <div className="text-sm text-muted">Confirm destructive</div>
        <select
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          className="h-8 w-40 rounded-md border border-border bg-panel px-2 text-xs"
        >
          <option value="always">Always (recommended)</option>
          <option value="cluster">For production clusters only</option>
          <option value="never">Never (not recommended)</option>
        </select>
      </div>
    </div>
  );
}

function useLocalStorage(
  key: string,
  initial: string,
): [string, (v: string) => void] {
  const [value, setValue] = useState<string>(() => {
    if (typeof window === "undefined") return initial;
    const raw = window.localStorage.getItem(key);
    return raw ?? initial;
  });
  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(key, value);
  }, [key, value]);
  return [value, setValue];
}
