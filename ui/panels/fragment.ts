import type { Result, TraceDetail } from "../contracts";
import type { DashboardSpec, PanelResult, VarValue } from "./types";

export type PanelFragment = {
  dashboard: DashboardSpec;
  results: PanelResult[];
  vars?: Record<string, VarValue>;
  trace?: Result<TraceDetail>;
};

export function panelFragment(value: unknown): PanelFragment {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Missing panel view");
  const f = value as Partial<PanelFragment>;
  if (f.dashboard?.version !== 1 || !Array.isArray(f.dashboard.panels) || !f.dashboard.panels.length || f.dashboard.panels.length > 40 || !Array.isArray(f.results)) throw new Error("Invalid panel view");
  if (f.dashboard.panels.some(p => !p || typeof p.id !== "string" || !p.id) || f.results.some(r => !r || typeof r.id !== "string")) throw new Error("Invalid panel view");
  const ids = new Set(f.dashboard.panels.map(p => p.id));
  if (ids.size !== f.dashboard.panels.length || f.results.length !== ids.size || new Set(f.results.map(r => r.id)).size !== ids.size || f.results.some(r => !ids.has(r.id) || !["ok", "empty", "error"].includes(r.status))) throw new Error("Panel results do not match the view");
  if (f.vars !== undefined && (!f.vars || typeof f.vars !== "object" || Array.isArray(f.vars) || Object.values(f.vars).some(v => typeof v !== "string" && !(Array.isArray(v) && v.every(x => typeof x === "string"))))) throw new Error("Invalid panel variables");
  if (f.trace !== undefined && (!f.trace?.data || !Array.isArray(f.trace.data.spans) || !Array.isArray(f.trace.data.logs) || !Array.isArray(f.trace.data.services))) throw new Error("Invalid trace detail");
  return f as PanelFragment;
}
