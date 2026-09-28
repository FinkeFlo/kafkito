import type { Message } from "@/lib/api";
import type { Token } from "@/lib/path-builder";
import { MessageRow } from "./MessageRow";

/** The rendered message rows. */
export function MessageList({
  messages,
  onPick,
}: {
  messages: Message[];
  onPick: (trail: Token[], leafValue: unknown) => void;
}) {
  return (
    <div className="divide-y divide-[var(--color-border)]">
      {messages.map((m) => (
        <MessageRow key={`${m.partition}-${m.offset}`} m={m} onPick={onPick} />
      ))}
    </div>
  );
}
