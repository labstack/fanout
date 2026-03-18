import type { ComparisonData } from "@/lib/types";

function formatNum(v: number): string {
  if (Math.abs(v) >= 10000) return `${(v / 1000).toFixed(1)}k`;
  if (Math.abs(v) >= 100) return v.toLocaleString("en-US", { maximumFractionDigits: 0 });
  if (Math.abs(v) >= 1) return v.toLocaleString("en-US", { maximumFractionDigits: 1 });
  if (Math.abs(v) >= 0.01) return v.toLocaleString("en-US", { maximumFractionDigits: 2 });
  return v.toLocaleString("en-US", { maximumFractionDigits: 3 });
}

export function ComparisonBlock({ data }: { data: ComparisonData }) {
  return (
    <div className="space-y-3">
      {/* Header bar */}
      <div className="flex items-center rounded-lg bg-muted/50 px-4 py-2.5">
        <div className="w-[130px] shrink-0" />
        <div className="w-[120px] shrink-0 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {data.leftLabel}
        </div>
        <div className="flex-1 text-center text-xs text-muted-foreground">Change</div>
        <div className="w-[120px] shrink-0 text-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {data.rightLabel}
        </div>
      </div>

      {/* Metric rows */}
      <div className="space-y-1.5">
        {data.metrics.map((m, i) => {
          const isRegression = m.direction === "regression";
          const isImprovement = m.direction === "improvement";
          const changeColor = isRegression
            ? "text-red-400"
            : isImprovement
              ? "text-emerald-400"
              : "text-zinc-500";
          const changeBg = isRegression
            ? "bg-red-500/10 border-red-500/30"
            : isImprovement
              ? "bg-emerald-500/10 border-emerald-500/30"
              : "bg-zinc-500/10 border-zinc-600/30";
          const arrow = m.changePct > 5 ? "\u2191" : m.changePct < -5 ? "\u2193" : "\u2192";
          const rightColor = isRegression ? "text-red-400" : isImprovement ? "text-emerald-400" : "text-foreground";

          return (
            <div key={i} className={`flex items-center rounded-lg border px-4 py-3 transition-colors hover:bg-muted/30 ${
              isRegression ? "border-red-500/15" : isImprovement ? "border-emerald-500/15" : "border-border"
            }`}>
              {/* Label */}
              <div className="w-[130px] shrink-0">
                <span className="text-sm font-medium text-foreground">{m.label}</span>
                {m.unit && <span className="text-xs text-muted-foreground ml-1.5">{m.unit}</span>}
              </div>

              {/* Left value */}
              <div className="w-[120px] shrink-0 text-center">
                <span className="text-xl font-bold tabular-nums text-foreground">
                  {formatNum(m.leftValue)}
                </span>
              </div>

              {/* Change pill */}
              <div className="flex-1 flex items-center justify-center gap-2">
                <div className={`inline-flex items-center gap-1 rounded-full border px-3 py-1 text-xs font-bold tabular-nums ${changeBg} ${changeColor}`}>
                  <span className="text-[11px]">{arrow}</span>
                  <span>{m.changePct > 0 ? "+" : ""}{m.changePct.toFixed(0)}%</span>
                </div>
                {m.significant && (
                  <span className={`text-[9px] font-bold uppercase tracking-widest px-1.5 py-0.5 rounded ${
                    isRegression ? "text-red-400/80 bg-red-500/10" : isImprovement ? "text-emerald-400/80 bg-emerald-500/10" : "text-zinc-500 bg-muted"
                  }`} title="Statistically significant">
                    sig
                  </span>
                )}
              </div>

              {/* Right value */}
              <div className="w-[120px] shrink-0 text-center">
                <span className={`text-xl font-bold tabular-nums ${rightColor}`}>
                  {formatNum(m.rightValue)}
                </span>
              </div>
            </div>
          );
        })}
      </div>

      {/* Verdict */}
      {data.verdict && (
        <div className="flex items-start gap-2 text-sm text-muted-foreground border-t border-border pt-3">
          <span className="shrink-0 mt-0.5 text-amber-400">&#9670;</span>
          {data.verdict}
        </div>
      )}
    </div>
  );
}
