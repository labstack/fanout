import { useState } from "react";
import type { ChangePoint } from "@/lib/types";

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
}

const MAX_VISIBLE = 5;

interface Props {
  changePoints: ChangePoint[];
}

export function ChangePointList({ changePoints }: Props) {
  const [expanded, setExpanded] = useState(false);

  if (!changePoints || changePoints.length === 0) return null;

  const visible = expanded ? changePoints : changePoints.slice(0, MAX_VISIBLE);
  const hasMore = changePoints.length > MAX_VISIBLE;

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">
        Change Points
        <span className="text-muted-foreground/60 ml-1 normal-case">({changePoints.length})</span>
      </div>
      <div className="space-y-1">
        {visible.map((cp, i) => {
          const ratio = cp.before > 0 ? cp.after / cp.before : 0;
          const direction = ratio > 1 ? "+" : "";
          const color = cp.metric.includes("error") ? "text-unhealthy" : "text-degraded";

          return (
            <div key={`${cp.time}-${cp.metric}-${i}`} className="flex items-center justify-between py-1 text-sm">
              <span className="text-primary mono text-xs">{fmtTime(cp.time)}</span>
              <span className={`mono text-xs ${color}`}>
                {cp.metric.replace(/_/g, " ")} {direction}{ratio.toFixed(1)}x
              </span>
            </div>
          );
        })}
      </div>
      {hasMore && (
        <button
          type="button"
          onClick={() => setExpanded(!expanded)}
          className="mt-2 text-[11px] text-muted-foreground hover:text-foreground mono transition-colors"
        >
          {expanded ? "Show less" : `Show ${changePoints.length - MAX_VISIBLE} more`}
        </button>
      )}
    </div>
  );
}
