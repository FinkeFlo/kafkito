import { CheckCircle2, CircleHelp, Equal, EqualNot, type LucideIcon } from "lucide-react";
import { Badge, type BadgeVariant } from "@/components/ui/Badge";
import type { Verdict } from "@/lib/message-compare";

const VERDICTS: Record<Verdict, { label: string; variant: BadgeVariant; Icon: LucideIcon }> = {
  identical: { label: "Identical", variant: "success", Icon: CheckCircle2 },
  equivalent: { label: "Equivalent", variant: "info", Icon: Equal },
  // A difference is a finding, not a failure: warning, not danger (§ 6.5).
  different: { label: "Different", variant: "warning", Icon: EqualNot },
  not_comparable: { label: "Not comparable", variant: "neutral", Icon: CircleHelp },
};

export function verdictLabel(v: Verdict): string {
  return VERDICTS[v].label;
}

/** The outcome of a comparison as a word with a shape-coded icon. */
export function VerdictBadge({ verdict }: { verdict: Verdict }) {
  const { label, variant, Icon } = VERDICTS[verdict];
  return (
    <Badge
      variant={variant}
      leadingIcon={<Icon aria-hidden="true" className="h-3.5 w-3.5" />}
      data-testid="verdict"
    >
      {label}
    </Badge>
  );
}
