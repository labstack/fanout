import type { Message } from "@ag-ui/client";
import type { Change } from "./dashboard-receipt";

export type PanelCheck = { id: string; status: string; rows?: number; diagnosis?: string; error?: string; elapsed_ms?: number };
export type SaveReceipt = {
  base_version: number; version: number; changes: Change[]; layout_changed: boolean; dashboard_fields?: string[];
  save_check: { checked: boolean; reason?: string; elapsed_ms: number; panels: PanelCheck[] };
};
export type DashboardToolResult = { id: string; name: string; version: number; label: string; receipt: SaveReceipt };
export const mutationNames = new Set(["create_dashboard", "edit_dashboard", "replace_dashboard", "restore_dashboard_version"]);
export function object(value: unknown): value is Record<string, unknown> { return !!value && typeof value === "object" && !Array.isArray(value); }
export function jsonObject(value: unknown): Record<string, unknown> | undefined {
  if (typeof value !== "string") return;
  try { const parsed: unknown = JSON.parse(value); if (object(parsed)) return parsed; } catch { /* incomplete tool arguments/results */ }
}
const strings = (v: unknown): v is string[] => Array.isArray(v) && v.every(s => typeof s === "string");
const duration = (v: unknown) => typeof v === "number" && Number.isFinite(v) && v >= 0;
export function panelCheck(v: unknown): v is PanelCheck {
  return object(v) && typeof v.id === "string" && ["ok", "empty", "error", "invalid", "not_run", "partial", "stale"].includes(String(v.status))
    && (v.rows === undefined || duration(v.rows)) && (v.elapsed_ms === undefined || duration(v.elapsed_ms))
    && (v.diagnosis === undefined || typeof v.diagnosis === "string") && (v.error === undefined || typeof v.error === "string");
}
function saveReceipt(v: unknown): v is SaveReceipt {
  if (!object(v) || !Number.isInteger(v.base_version) || Number(v.base_version) < 0 || !Number.isInteger(v.version) || Number(v.version) < 1
    || !Array.isArray(v.changes) || typeof v.layout_changed !== "boolean" || (v.dashboard_fields !== undefined && !strings(v.dashboard_fields))) return false;
  if (!v.changes.every(c => object(c) && typeof c.panel_id === "string" && typeof c.title === "string" && ["added", "changed", "removed"].includes(String(c.kind))
    && (c.fields === undefined || strings(c.fields) && !c.fields.includes("grid")) && (c.position_changed === undefined || typeof c.position_changed === "boolean"))) return false;
  const check = v.save_check;
  return object(check) && typeof check.checked === "boolean" && duration(check.elapsed_ms) && Array.isArray(check.panels) && check.panels.every(p => panelCheck(p) && Number.isInteger(p.rows) && Number(p.rows) >= 0) && new Set(check.panels.map(p => p.id)).size === check.panels.length
    && (check.reason === undefined || typeof check.reason === "string") && (!check.checked || check.panels.every(p => p.status !== "not_run" && p.status !== "invalid"));
}

/** Results are joined to a mutation call inside their own user turn, including
 * persisted transcripts. A dashboard-shaped read or narration is never a save. */
export function dashboardToolResult(toolCallId: string, content: unknown, messages: readonly Message[]): DashboardToolResult | null {
  let resultIndex = -1;
  messages.forEach((m,i) => { if (m.role === "tool" && m.toolCallId === toolCallId && m.content === content) resultIndex = i; });
  const end = resultIndex < 0 ? messages.length : resultIndex;
  let start = -1; messages.slice(0,end).forEach((m,i) => { if (m.role === "user") start = i; });
  if (start < 0) return null;
  const calls = messages.slice(start + 1, end).flatMap(m => m.role === "assistant" ? m.toolCalls ?? [] : []).filter(c => c.id === toolCallId);
  const name = calls.length === 1 ? calls[0].function.name : undefined;
  if (!name || !mutationNames.has(name)) return null;
  const payload = jsonObject(content), record = payload?.dashboard, receipt = payload?.receipt;
  if (!payload || payload.error || payload.isError || !object(record) || typeof record.id !== "string" || !record.id || typeof record.name !== "string" || !record.name
    || !saveReceipt(receipt) || record.version !== receipt.version || resultIndex >= 0 && (messages[resultIndex] as Extract<Message, { role: "tool" }>).error) return null;
  return { id: record.id, name: record.name, version: receipt.version, label: name === "create_dashboard" ? "Created" : name === "restore_dashboard_version" ? "Restored" : "Updated", receipt };
}
