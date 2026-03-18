import type { LogsBlockData, LogEntry } from "@/lib/types";

const SEVERITY_STYLES: Record<string, { badge: string; text: string }> = {
  fatal: { badge: "bg-red-500/20 text-red-400 border-red-500/30", text: "text-red-300" },
  error: { badge: "bg-red-500/15 text-red-400 border-red-500/25", text: "text-red-300" },
  warn: { badge: "bg-amber-500/15 text-amber-400 border-amber-500/25", text: "text-amber-200" },
  info: { badge: "bg-blue-500/10 text-blue-400 border-blue-500/20", text: "text-zinc-300" },
  debug: { badge: "bg-zinc-500/10 text-zinc-500 border-zinc-500/20", text: "text-zinc-500" },
  trace: { badge: "bg-zinc-500/10 text-zinc-600 border-zinc-600/20", text: "text-zinc-600" },
};

const defaultStyle = { badge: "bg-zinc-500/10 text-zinc-400 border-zinc-500/20", text: "text-zinc-400" };

function getStyle(severity: string) {
  return SEVERITY_STYLES[severity.toLowerCase()] ?? defaultStyle;
}

function formatTime(time: string): string {
  if (time.includes("T")) {
    const d = new Date(time);
    if (!isNaN(d.getTime())) {
      return d.toLocaleTimeString("en-US", { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" })
        + "." + String(d.getMilliseconds()).padStart(3, "0");
    }
  }
  return time;
}

function EntryRow({ entry, onAction }: { entry: LogEntry; onAction?: (prompt: string) => void }) {
  const style = getStyle(entry.severity);
  const isHighSev = ["error", "fatal"].includes(entry.severity.toLowerCase());
  const isWarn = entry.severity.toLowerCase() === "warn";

  return (
    <div className={`group flex items-start gap-3 px-3 py-1.5 hover:bg-white/[0.03] border-l-2 ${
      isHighSev ? "border-l-red-500/40" : isWarn ? "border-l-amber-500/30" : "border-l-transparent"
    }`}>
      <span className="shrink-0 font-mono text-[11px] text-zinc-600 pt-0.5 w-[90px]">
        {formatTime(entry.time)}
      </span>

      <span className={`shrink-0 inline-flex items-center justify-center w-[52px] rounded border text-[10px] font-bold uppercase tracking-wider py-0.5 ${style.badge}`}>
        {entry.severity.length > 5 ? entry.severity.slice(0, 5) : entry.severity}
      </span>

      <span className="shrink-0 font-mono text-[11px] text-zinc-500 pt-0.5 w-[120px] truncate" title={entry.service}>
        {entry.service}
      </span>

      <span className={`flex-1 text-[12px] leading-relaxed break-words ${style.text}`}>
        {entry.body}
      </span>

      {entry.traceId && (
        <button
          className="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity text-[10px] font-mono text-blue-400/60 hover:text-blue-400 hover:underline cursor-pointer pt-0.5"
          onClick={() => onAction?.(`Show trace ${entry.traceId}`)}
          title={`Trace ${entry.traceId}`}
        >
          {entry.traceId.slice(0, 12)}
        </button>
      )}
    </div>
  );
}

export function LogsBlock({ data, onAction }: { data: LogsBlockData; onAction?: (prompt: string) => void }) {
  if (data.entries.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-zinc-950 p-4 text-sm text-zinc-500 font-mono">
        No log entries to display.
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950 overflow-hidden">
      <div className="flex items-center justify-between px-3 py-2 border-b border-zinc-800/80 bg-zinc-900/50">
        <span className="text-[11px] text-zinc-500 font-medium uppercase tracking-wider">Log Entries</span>
        <span className="text-[11px] text-zinc-600">{data.entries.length} entries</span>
      </div>

      <div className="overflow-auto font-mono" style={{ maxHeight: 400 }}>
        {data.entries.map((entry, i) => (
          <EntryRow key={i} entry={entry} onAction={onAction} />
        ))}
      </div>
    </div>
  );
}
