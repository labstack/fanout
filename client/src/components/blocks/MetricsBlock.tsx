import type { MetricsBlockData } from "@/lib/types";
import { fmt } from "@/lib/utils";

const STATUS_COLORS: Record<string, string> = {
  ok: "#22c55e",
  warning: "#f59e0b",
  danger: "#ef4444",
};

const DEFAULT_ACCENT = "#818cf8";

export function MetricsBlock({ data }: { data: MetricsBlockData }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {data.items.map((item, i) => {
        const accent = STATUS_COLORS[item.status] ?? DEFAULT_ACCENT;
        return (
          <div
            key={i}
            className="block-card"
            style={{ borderColor: `${accent}26` }}
          >
            <div
              className="flex items-center gap-1.5 block-title mb-2"
              style={{ color: accent }}
            >
              <span
                className="inline-block h-1.5 w-1.5 rounded-full"
                style={{ backgroundColor: accent }}
              />
              {item.label}
            </div>
            <div className="text-2xl font-bold text-foreground">
              {fmt(item.value)}
              <span className="text-sm font-normal text-muted-foreground ml-1">
                {item.unit}
              </span>
            </div>
          </div>
        );
      })}
    </div>
  );
}
