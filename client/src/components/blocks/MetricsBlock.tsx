import { Card, CardContent } from "@/components/ui/card";
import type { MetricsBlockData, MetricItem } from "@/lib/types";

const statusClasses: Record<MetricItem["status"], { dot: string; border: string }> = {
  ok: {
    dot: "bg-emerald-500",
    border: "border-l-emerald-500",
  },
  warning: {
    dot: "bg-amber-500",
    border: "border-l-amber-500",
  },
  danger: {
    dot: "bg-red-500",
    border: "border-l-red-500",
  },
};

export function MetricsBlock({ data }: { data: MetricsBlockData }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {data.items.map((item, i) => {
        const sc = statusClasses[item.status];
        return (
          <Card key={i} className={`border-l-4 ${sc.border} py-4 gap-2`}>
            <CardContent className="p-0 px-4">
              <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
                <span className={`inline-block h-2 w-2 rounded-full ${sc.dot}`} />
                {item.label}
              </div>
              <div className="text-2xl font-bold">
                {item.value}
                <span className="text-sm font-normal text-muted-foreground ml-1">
                  {item.unit}
                </span>
              </div>
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
