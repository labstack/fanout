import type { EndpointsData } from "@/lib/types";

const METHOD_COLORS: Record<string, string> = {
  GET: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  POST: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  PUT: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  PATCH: "bg-orange-500/15 text-orange-600 dark:text-orange-400",
  DELETE: "bg-red-500/15 text-red-600 dark:text-red-400",
  HEAD: "bg-purple-500/15 text-purple-600 dark:text-purple-400",
  OPTIONS: "bg-gray-500/15 text-gray-600 dark:text-gray-400",
};

function methodBadge(method: string): string {
  return METHOD_COLORS[method.toUpperCase()] ?? METHOD_COLORS.GET;
}

function statusPill(
  status: string,
  errorRate: number,
): { label: string; className: string } {
  if (status === "unhealthy" || errorRate > 1) {
    return {
      label: "UNHEALTHY",
      className: "bg-red-500/12 text-red-500",
    };
  }
  if (status === "degraded") {
    return {
      label: "DEGRADED",
      className: "bg-amber-500/12 text-amber-500",
    };
  }
  if (errorRate > 0.3) {
    return {
      label: "WATCH",
      className: "bg-amber-500/12 text-amber-500",
    };
  }
  return {
    label: "OK",
    className: "bg-emerald-500/12 text-emerald-500",
  };
}

function latencyColor(value: number, p95Threshold: number, p99Threshold: number): string {
  if (value > p99Threshold) return "text-red-500";
  if (value > p95Threshold) return "text-amber-500";
  return "";
}

export function EndpointsBlock({ data }: { data: EndpointsData }) {
  if (data.endpoints.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No endpoint data to display.
      </div>
    );
  }

  return (
    <div className="overflow-x-auto block-card-flush">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border bg-muted/50">
            <th className="px-4 py-2.5 text-left font-medium text-muted-foreground">
              Endpoint
            </th>
            <th className="px-4 py-2.5 text-right font-medium text-muted-foreground">
              RPM
            </th>
            <th className="px-4 py-2.5 text-right font-medium text-muted-foreground">
              P50
            </th>
            <th className="px-4 py-2.5 text-right font-medium text-muted-foreground">
              P95
            </th>
            <th className="px-4 py-2.5 text-right font-medium text-muted-foreground">
              P99
            </th>
            <th className="px-4 py-2.5 text-right font-medium text-muted-foreground">
              Err%
            </th>
            <th className="px-4 py-2.5 text-center font-medium text-muted-foreground">
              Status
            </th>
          </tr>
        </thead>
        <tbody>
          {data.endpoints.map((ep, i) => {
            const pill = statusPill(ep.status, ep.errorRate);

            return (
              <tr
                key={i}
                className="border-b border-border last:border-b-0 transition-colors hover:bg-muted/50"
              >
                <td className="px-4 py-2">
                  <span className="flex items-center gap-2">
                    <span
                      className={`inline-block rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase ${methodBadge(ep.method)}`}
                    >
                      {ep.method}
                    </span>
                    <code className="text-foreground text-xs">{ep.path}</code>
                  </span>
                </td>
                <td className="px-4 py-2 text-right font-mono text-xs">
                  {ep.rpm}
                </td>
                <td className="px-4 py-2 text-right font-mono text-xs">
                  {ep.p50}ms
                </td>
                <td
                  className={`px-4 py-2 text-right font-mono text-xs ${latencyColor(ep.p95, 200, 500)}`}
                >
                  {ep.p95}ms
                </td>
                <td
                  className={`px-4 py-2 text-right font-mono text-xs ${latencyColor(ep.p99, 200, 500)}`}
                >
                  {ep.p99}ms
                </td>
                <td
                  className={`px-4 py-2 text-right font-mono text-xs ${ep.errorRate > 1 ? "text-red-500" : ep.errorRate > 0.3 ? "text-amber-500" : ""}`}
                >
                  {ep.errorRate}%
                </td>
                <td className="px-4 py-2 text-center">
                  <span
                    className={`inline-block rounded-full px-2 py-0.5 text-[10px] font-semibold ${pill.className}`}
                  >
                    {pill.label}
                  </span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
