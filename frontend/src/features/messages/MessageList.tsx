import type { Message } from "@/lib/api";
import type { Token } from "@/lib/path-builder";
import { MessageRow } from "./MessageRow";
import type { FieldFilter } from "./use-click-to-filter";

/** The rendered message rows. */
export function MessageList({
  messages,
  onPick,
  onFilter,
}: {
  messages: Message[];
  onPick: (trail: Token[], leafValue: unknown) => void;
  onFilter: (filter: FieldFilter) => void;
}) {
  return (
    <div className="divide-y divide-border">
      {messages.map((m) => (
        <MessageRow key={`${m.partition}-${m.offset}`} m={m} onPick={onPick} onFilter={onFilter} />
      ))}
    </div>
  );
}
