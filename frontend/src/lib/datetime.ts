export function pad(n: number, width = 2): string {
  return String(n).padStart(width, "0");
}

/** Epoch ms → "YYYY-MM-DDTHH:mm:ss" in the browser's local time for the picker. */
export function msToLocalInput(ms: number): string {
  const d = new Date(ms);
  if (Number.isNaN(d.getTime())) return "";
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  );
}

/** datetime-local value (local time) → epoch ms. NaN when empty/invalid. */
export function localInputToMs(value: string): number {
  if (!value) return Number.NaN;
  return new Date(value).getTime();
}
