import type { EventEmitter } from "node:events";

export function diagnosticTail(output: string): string {
  return output
    .replace(/fo_[A-Za-z0-9_.%~+\/=-]+/g, "[REDACTED]")
    .replace(/setup_token=[^\s&"'<>]+/gi, "setup_token=[REDACTED]")
    .replace(/((?:set-cookie|cookie)["']?\s*[:=]\s*)[^\r\n]+/gi, "$1[REDACTED]")
    .trimEnd().split(/\r?\n/).slice(-50).join("\n");
}

export async function safeRequest<T extends { ok(): boolean; status(): number }>(path: string, send: () => Promise<T>): Promise<T> {
  let response: T;
  try { response = await send(); }
  catch { throw new Error(`Request ${path} failed`); }
  if (!response.ok()) throw new Error(`HTTP ${response.status()} ${path}`);
  return response;
}

export async function waitUntilReady(deadline: number, probe: () => Promise<string | undefined>, interval = 500): Promise<void> {
  let lastCause = "No readiness response";
  while (Date.now() < deadline) {
    try {
      const cause = await probe();
      if (cause === undefined) return;
      lastCause = cause;
    } catch (error) { lastCause = error instanceof Error ? error.message : "Readiness request failed"; }
    await new Promise(resolve => setTimeout(resolve, Math.min(interval, Math.max(0, deadline - Date.now()))));
  }
  throw new Error(`Readiness timed out: ${lastCause}`);
}

type ReadinessResult = { id: string; status: string; frame?: { rows: number; columns: { name: string }[]; values: unknown[][] } };
export function readinessCause(body: { results?: ReadinessResult[] }): string | undefined {
  const results = body.results;
  const error = results?.find(result => result.status === "error");
  if (error) return `Panel ${error.id} status error`;
  for (const id of ["count", "log_count"]) {
    const result = results?.find(result => result.id === id);
    if (result?.status !== "ok" || !result.frame?.rows || !result.frame.values.some(col => col.some(value => typeof value === "number" && value > 0))) return `Panel ${id} has no seeded count`;
  }
  const map = results?.find(result => result.id === "map");
  const kind = map?.frame?.columns.findIndex(column => column.name === "kind") ?? -1;
  const kinds = map?.frame?.values[kind] ?? [];
  if (map?.status !== "ok" || kinds.filter(value => value === "node").length < 6) return "Panel map has fewer than six nodes";
  if (!kinds.some(value => value === "edge")) return "Panel map has no topology edge";
}

export function installSignalCleanup(cleanup: () => void, emitter: Pick<EventEmitter, "once" | "removeListener"> = process, raise: (signal: "SIGINT" | "SIGTERM") => void = signal => { process.kill(process.pid, signal); }) {
  const remove = () => {
    emitter.removeListener("SIGINT", interrupt);
    emitter.removeListener("SIGTERM", terminate);
    emitter.removeListener("exit", cleanup);
  };
  const handle = (signal: "SIGINT" | "SIGTERM") => {
    remove();
    try { cleanup(); } finally { raise(signal); }
  };
  const interrupt = () => handle("SIGINT");
  const terminate = () => handle("SIGTERM");
  emitter.once("SIGINT", interrupt);
  emitter.once("SIGTERM", terminate);
  emitter.once("exit", cleanup);
  return remove;
}

export function chartLabel(title: string, viz: string, overview = false): RegExp | undefined {
  const escaped = title.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const types: Record<string, string> = { gauge: "gauge", timeseries: "time series", bar: "bar chart" };
  const analysis = ["heatmap", "histogram", "scatter", "state_timeline"].includes(viz);
  if (!types[viz] && !analysis) return;
  const prefix = analysis ? `${escaped}: [1-9]\\d* rows; .+` : `${escaped}: ${types[viz]}`;
  return new RegExp(`^${prefix}${overview ? ": [1-9]\\d* series(?:;|$)" : "(?: Brush|$)"}`);
}
