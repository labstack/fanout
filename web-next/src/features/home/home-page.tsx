import { useOverview } from "./use-overview";
import { LoadingState } from "@/components/states/loading-state";
import { ErrorState } from "@/components/states/error-state";
import { EmptyState } from "@/components/states/empty-state";

export function HomePage() {
  const { data, isPending, isError, refetch } = useOverview();

  if (isPending) return <LoadingState label="Loading system overview…" />;
  if (isError || !data) return <ErrorState message="Couldn't load the overview." onRetry={() => void refetch()} />;
  if (data.services.length === 0)
    return <EmptyState title="Waiting for data" hint="Point OTLP at this instance to see your services." />;

  const { total_services, throughput_per_min, global_error_rate } = data.health;
  return (
    <section className="flex items-baseline gap-2">
      <span className="text-ok-text" aria-hidden>●</span>
      <h1 className="text-[22px] font-semibold tracking-tight tnum">
        {total_services} services healthy
      </h1>
      <span className="text-ink-2 tnum">
        · {Math.round(throughput_per_min / 60).toLocaleString()} req/s
        · {(global_error_rate * 100).toFixed(2)}% errors
      </span>
    </section>
  );
}
