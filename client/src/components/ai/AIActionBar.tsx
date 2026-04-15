import { BellPlus, GitCompareArrows, Search, Sparkles } from "lucide-react";

export type AIActionKind = "explain" | "drill" | "compare" | "alert";

export interface AIAction {
  label: string;
  prompt: string;
  kind: AIActionKind;
}

const ICONS = {
  explain: Sparkles,
  drill: Search,
  compare: GitCompareArrows,
  alert: BellPlus,
} as const;

export function AIActionBar({
  actions,
  onAction,
  className = "",
}: {
  actions: AIAction[];
  onAction: (prompt: string) => void;
  className?: string;
}) {
  if (actions.length === 0) return null;

  return (
    <div
      className={`rounded-xl border border-border/60 bg-surface-1/70 px-3 py-2 ${className}`.trim()}
    >
      <div className="mb-2 flex items-center gap-2">
        <Sparkles className="h-3.5 w-3.5 text-primary" />
        <span className="mono text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
          AI actions
        </span>
      </div>
      <div className="flex flex-wrap gap-2">
        {actions.map((action) => {
          const Icon = ICONS[action.kind];
          return (
            <button
              key={`${action.kind}:${action.label}`}
              type="button"
              onClick={() => onAction(action.prompt)}
              className="inline-flex items-center gap-1.5 rounded-full border border-primary/15 bg-surface-2 px-3 py-1.5 text-[11px] text-muted-foreground transition-colors hover:border-primary/30 hover:text-foreground"
            >
              <Icon className="h-3 w-3 text-primary/80" />
              <span>{action.label}</span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
