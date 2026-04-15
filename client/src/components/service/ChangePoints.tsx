import type { ChangePoint } from "@/lib/types";

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
}

interface Props {
  changePoints: ChangePoint[];
}

export function ChangePointList({ changePoints }: Props) {
  if (!changePoints || changePoints.length === 0) return null;

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">Change Points</div>
      <div className="space-y-1">
        {changePoints.map((cp, i) => {
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
    </div>
  );
}
