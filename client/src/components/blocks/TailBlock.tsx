import { useRef, useEffect } from "react";
import type { TailData, LogEntry } from "@/lib/types";

const SEVERITY_COLORS: Record<string, string> = {
  error: "text-red-400",
  warn: "text-amber-400",
  info: "text-blue-400",
  debug: "text-zinc-500",
};

function severityClass(severity: string): string {
  return SEVERITY_COLORS[severity.toLowerCase()] ?? "text-zinc-500";
}

function formatTimestamp(time: number): string {
  // Handle both seconds and milliseconds
  const ms = time > 1e12 ? time : time * 1000;
  const d = new Date(ms);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  const sss = String(d.getMilliseconds()).padStart(3, "0");
  return `${hh}:${mm}:${ss}.${sss}`;
}

function EntryLine({ entry, onAction }: { entry: LogEntry; onAction?: (prompt: string) => void }) {
  const sevCls = severityClass(entry.severity);

  return (
    <div className="flex gap-2 leading-5 hover:bg-white/5">
      <span className="shrink-0 text-zinc-500">{formatTimestamp(entry.time)}</span>
      <span className={`shrink-0 w-12 text-right uppercase font-semibold ${sevCls}`}>
        {entry.severity}
      </span>
      <span className="shrink-0 text-zinc-500">{entry.service}</span>
      <span className="break-all">{entry.body}</span>
      {entry.traceId && (
        <button
          className="shrink-0 ml-auto text-blue-400/70 hover:text-blue-400 hover:underline cursor-pointer"
          onClick={() => onAction?.(`Show trace ${entry.traceId}`)}
          title={`Trace ${entry.traceId}`}
        >
          {entry.traceId.slice(0, 8)}
        </button>
      )}
    </div>
  );
}

export function TailBlock({ data, onAction }: { data: TailData; onAction?: (prompt: string) => void }) {
  const scrollRef = useRef<HTMLDivElement>(null);

  // Auto-scroll to bottom when new entries arrive
  useEffect(() => {
    const el = scrollRef.current;
    if (el) {
      el.scrollTop = el.scrollHeight;
    }
  }, [data.entries]);

  if (data.entries.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-zinc-950 p-4 text-sm text-zinc-500 font-mono">
        No log entries to display.
      </div>
    );
  }

  return (
    <div
      ref={scrollRef}
      className="overflow-auto rounded-lg border border-zinc-800 bg-zinc-950 text-zinc-300 font-mono text-xs p-3"
      style={{ maxHeight: 300 }}
    >
      {data.entries.map((entry, i) => (
        <EntryLine key={i} entry={entry} onAction={onAction} />
      ))}
    </div>
  );
}
