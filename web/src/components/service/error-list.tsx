import type { ErrorInfo } from "@/lib/types";

interface Props {
  errors: ErrorInfo[];
  onClickTrace: (traceID: string) => void;
}

export function ErrorList({ errors, onClickTrace }: Props) {
  if (!errors || errors.length === 0) return null;

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-3">Top Errors</div>
      <div className="space-y-2">
        {errors.slice(0, 10).map((err, i) => (
          <div key={`${err.operation}-${i}`} className="flex items-baseline gap-3 text-sm">
            <span className="mono text-muted-foreground tabular-nums w-12 text-right shrink-0">
              {err.count.toLocaleString()}
            </span>
            <div className="min-w-0 flex-1">
              <div className="text-foreground/80 mono text-xs truncate" title={err.message}>
                {err.message}
              </div>
              <div className="flex items-center gap-2 text-[10px] text-muted-foreground mt-0.5">
                <span>{err.operation}</span>
                {err.trace_id && (
                  <button
                    type="button"
                    onClick={() => onClickTrace(err.trace_id)}
                    className="text-primary hover:underline"
                  >
                    {err.trace_id.slice(0, 8)}
                  </button>
                )}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
