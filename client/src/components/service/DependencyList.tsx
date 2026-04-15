import { Link } from "react-router";
import type { ServiceDependency } from "@/lib/types";

function fmtMs(v: number): string {
  return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v.toFixed(0)}ms`;
}

function fmtPercent(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

function fmtRate(v: number): string {
  return v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v.toFixed(0);
}

interface Props {
  dependencies: ServiceDependency[];
}

export function DependencyList({ dependencies }: Props) {
  if (!dependencies || dependencies.length === 0) return null;

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">Dependencies</div>
      <div className="space-y-1">
        {dependencies.map((dep) => (
          <Link
            key={dep.service}
            to={`/service/${encodeURIComponent(dep.service)}`}
            className="flex items-center justify-between py-2 px-1 rounded-md hover:bg-surface-2 transition-colors group"
          >
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground">{"\u2192"}</span>
              <span className="text-sm text-foreground/90 group-hover:text-foreground mono">
                {dep.service}
              </span>
            </div>
            <div className="flex items-center gap-4 text-xs mono">
              <span className="text-muted-foreground">{fmtRate(dep.call_count)}/min</span>
              <span className="text-muted-foreground">{fmtMs(dep.avg_ms)}</span>
              <span className={dep.error_rate > 0.01 ? "text-unhealthy" : "text-foreground/70"}>
                {fmtPercent(dep.error_rate)}
              </span>
            </div>
          </Link>
        ))}
      </div>
    </div>
  );
}
