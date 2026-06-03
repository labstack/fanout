import { useLocation, useNavigate } from "react-router";
import type { RecentError } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";

function truncate(msg: string, max = 48): string {
  return msg.length <= max ? msg : msg.slice(0, max) + "…";
}

interface Props {
  errors: RecentError[];
  /** The scan ran but failed — show "unavailable" instead of a false all-clear. */
  unavailable?: boolean;
}

export function RecentErrors({ errors, unavailable = false }: Props) {
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? undefined;

  const investigate = (e: RecentError) => {
    const prompt = `Investigate ${e.service} — errors matching "${e.message}" in the last 5 minutes (${e.count} occurrences). What's causing them?`;
    navigate(buildChatPath(prompt, token));
  };

  return (
    <div className="rounded-xl border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-2">Recent errors · last 5m</div>
      {unavailable ? (
        <div className="py-1 text-[12px] text-warning">
          Recent errors unavailable — retrying
        </div>
      ) : errors.length === 0 ? (
        <div className="py-1 text-[12px] text-muted-foreground">
          No errors in last 5m
        </div>
      ) : (
        <div className="space-y-0.5">
          {errors.map((e) => (
            <button
              key={`${e.service}:${e.message}`}
              type="button"
              onClick={() => investigate(e)}
              className="flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left text-[12px] transition-colors hover:bg-surface-2"
              title={`${e.service}: ${e.message}`}
            >
              <span className="shrink-0 font-mono text-[10.5px] font-medium text-foreground/80">
                {e.service}
              </span>
              <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground">
                {truncate(e.message)}
              </span>
              <span className="shrink-0 font-mono tabular-nums text-muted-foreground">
                {e.count.toLocaleString()}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
