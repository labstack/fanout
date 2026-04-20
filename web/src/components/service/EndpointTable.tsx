import type { ServiceEndpoint } from "@/lib/types";

function fmtMs(v: number): string {
  if (v >= 60000) return `${(v / 60000).toFixed(1)}m`;
  if (v >= 1000) return `${(v / 1000).toFixed(1)}s`;
  if (v < 1 && v > 0) return `<1ms`;
  return `${v.toFixed(0)}ms`;
}

function fmtPercent(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

function fmtRate(v: number): string {
  return v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v.toFixed(0);
}

interface Props {
  endpoints: ServiceEndpoint[];
  windowMinutes: number;
  onClickEndpoint: (op: string) => void;
}

export function EndpointTable({ endpoints, windowMinutes, onClickEndpoint }: Props) {
  if (endpoints.length === 0) return null;

  const sorted = [...endpoints].sort((a, b) => b.error_rate - a.error_rate);

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">Top Endpoints</div>
      <div className="overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr>
              <th className="text-left p-2">Operation</th>
              <th className="text-right p-2">Rate</th>
              <th className="text-right p-2">Errors</th>
              <th className="text-right p-2">P50</th>
              <th className="text-right p-2">P95</th>
            </tr>
          </thead>
          <tbody>
            {sorted.slice(0, 20).map((ep) => (
              <tr
                key={ep.operation}
                className="cursor-pointer hover:bg-surface-2 transition-colors"
                onClick={() => onClickEndpoint(ep.operation)}
              >
                <td className="p-2 text-sm text-foreground/90 mono truncate max-w-[300px]">
                  {ep.operation}
                </td>
                <td className="p-2 text-right text-xs text-muted-foreground mono">
                  {fmtRate(ep.count / windowMinutes)}/min
                </td>
                <td className={`p-2 text-right text-xs mono ${ep.error_rate > 0.05 ? "text-unhealthy" : ep.error_rate > 0.01 ? "text-degraded" : "text-foreground/70"}`}>
                  {fmtPercent(ep.error_rate)}
                </td>
                <td className="p-2 text-right text-xs text-foreground/70 mono">
                  {fmtMs(ep.p50_ms)}
                </td>
                <td className={`p-2 text-right text-xs mono ${ep.p95_ms > 1000 ? "text-degraded" : "text-foreground/70"}`}>
                  {fmtMs(ep.p95_ms)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
