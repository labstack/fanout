import { useMemo } from "react";
import type { DepMatrixData } from "@/lib/types";

function cellBg(errorRate: number | undefined): string {
  if (errorRate === undefined || errorRate === null) return "";
  if (errorRate === 0) return "rgba(34, 197, 94, 0.15)";
  if (errorRate < 0.5) return "rgba(34, 197, 94, 0.3)";
  if (errorRate < 1.0) return "rgba(234, 179, 8, 0.3)";
  if (errorRate < 2.0) return "rgba(234, 179, 8, 0.6)";
  return "rgba(239, 68, 68, 0.6)";
}

function errorColor(errorRate: number): string {
  if (errorRate > 1) return "#ef4444";
  if (errorRate > 0.5) return "#f59e0b";
  return "inherit";
}

export function DepMatrixBlock({ data }: { data: DepMatrixData }) {
  const lookup = useMemo(() => {
    const map = new Map<
      string,
      { errorRate: number; rpm: number; p95: number }
    >();
    for (const c of data.cells) {
      map.set(`${c.from}\u2192${c.to}`, c);
    }
    return map;
  }, [data.cells]);

  if (data.services.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No dependency data to display.
      </div>
    );
  }

  return (
    <div>
      {/* Header labels */}
      <div className="mb-2 flex items-center gap-2 text-[10px] text-muted-foreground uppercase tracking-wider">
        <span>Caller (rows)</span>
        <span className="text-foreground">{"\u2192"}</span>
        <span>Callee (columns)</span>
      </div>

      <div
        className="overflow-x-auto rounded-[14px]"
        style={{ background: "#111113", border: "1px solid rgba(129,140,248,0.15)" }}
      >
        <table className="w-auto text-xs border-collapse">
          <thead>
            <tr className="bg-muted/50">
              <th className="px-3 py-2 text-left font-medium text-muted-foreground border-b border-border sticky left-0 bg-muted/50 z-10">
                From \ To
              </th>
              {data.services.map((svc) => (
                <th
                  key={svc}
                  className="px-2 py-2 text-center font-medium text-muted-foreground border-b border-border"
                >
                  <span className="inline-block max-w-[80px] truncate">
                    {svc}
                  </span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {data.services.map((from) => (
              <tr key={from} className="border-b border-border last:border-b-0">
                <td className="px-3 py-2 font-medium text-foreground sticky left-0 bg-background z-10 border-r border-border">
                  <span className="inline-block max-w-[100px] truncate">
                    {from}
                  </span>
                </td>
                {data.services.map((to) => {
                  if (from === to) {
                    return (
                      <td
                        key={to}
                        className="px-2 py-2 text-center"
                        style={{ background: "var(--muted)", opacity: 0.3 }}
                      >
                        <span className="text-muted-foreground">&mdash;</span>
                      </td>
                    );
                  }

                  const cell = lookup.get(`${from}\u2192${to}`);
                  if (!cell) {
                    return (
                      <td
                        key={to}
                        className="px-2 py-2 text-center text-muted-foreground/40"
                      >
                        &middot;
                      </td>
                    );
                  }

                  return (
                    <td
                      key={to}
                      className="px-2 py-2 text-center group relative cursor-default"
                      style={{ backgroundColor: cellBg(cell.errorRate) }}
                    >
                      <div
                        className="font-mono text-[11px]"
                        style={{ color: errorColor(cell.errorRate) }}
                      >
                        {cell.errorRate}%
                      </div>
                      <div className="text-muted-foreground text-[9px]">
                        {cell.rpm} rpm
                      </div>
                      {/* Hover detail */}
                      <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 hidden group-hover:block z-50 rounded-md border border-border bg-popover px-2 py-1.5 text-[10px] text-popover-foreground shadow-md whitespace-nowrap">
                        <div className="font-medium mb-0.5">
                          {from} {"\u2192"} {to}
                        </div>
                        <div>
                          Error: {cell.errorRate}% | RPM: {cell.rpm} | P95:{" "}
                          {cell.p95}ms
                        </div>
                      </div>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Legend */}
      <div className="mt-2 flex flex-wrap gap-3 px-1">
        {[
          { color: "rgba(34,197,94,0.3)", label: "< 0.5%" },
          { color: "rgba(234,179,8,0.3)", label: "0.5-1%" },
          { color: "rgba(234,179,8,0.6)", label: "1-2%" },
          { color: "rgba(239,68,68,0.6)", label: "> 2%" },
        ].map((item) => (
          <div key={item.label} className="flex items-center gap-1.5 text-xs">
            <span
              className="inline-block h-2.5 w-2.5 rounded-sm border border-border"
              style={{ backgroundColor: item.color }}
            />
            <span className="text-muted-foreground">{item.label}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
