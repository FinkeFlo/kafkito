import { useState } from "react";
import type { TimeRangeMode } from "./time-range";

/** Selection state of a time-range control (resolve it with computeTimeRange). */
export function useTimeRangeState() {
  const [mode, setMode] = useState<TimeRangeMode>("off");
  const [preset, setPreset] = useState<string>("24h");
  const [customFrom, setCustomFrom] = useState<string>("");
  const [customTo, setCustomTo] = useState<string>("");

  return { mode, preset, customFrom, customTo, setMode, setPreset, setCustomFrom, setCustomTo };
}

export type TimeRangeState = ReturnType<typeof useTimeRangeState>;
