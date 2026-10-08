import type { Message } from "@ag-ui/client";
import { dashboardToolResult, jsonObject, mutationNames, object, panelCheck, type DashboardToolResult, type PanelCheck } from "./dashboard-tool-result";

export type Change={panel_id:string;title:string;kind:'added'|'changed'|'removed';fields?:string[];position_changed?:boolean};
const changeLabel=(c:Change):string=>c.kind==='added'?`+ ${c.title}`:c.kind==='removed'?`− ${c.title}`:`~ ${c.panel_id}${c.fields?.length?'.'+c.fields.join(', '):''}${c.position_changed?' · order':''}`;
export function changeChips(receipt: { changes: Change[]; layout_changed: boolean; dashboard_fields?: string[] }): string[] {
  return [...receipt.changes.map(changeLabel), ...(receipt.layout_changed ? ["Layout adjusted"] : []), ...(receipt.dashboard_fields?.map(field => `Dashboard: ${field}`) ?? [])];
}
export type Correction={panel_id:string;path:string;message:string};
function resolvedProblems(before:Correction[],after:Correction[],retainedIDs:Set<string>):Correction[] {
  return before.filter(p=>retainedIDs.has(p.panel_id) && !after.some(q=>q.panel_id===p.panel_id && q.path===p.path));
}
export type Stage = { state: "unobserved" | "incomplete" | "failed" | "complete" | "interrupted"; elapsed_ms?: number };
export type BuildReceipt = {
  turn_id: string; stages: Record<"schema" | "context" | "draft" | "validation" | "preview" | "save", Stage>;
  panel_count?: number; panels: PanelCheck[]; problems: Correction[]; corrections: Correction[];
  save_attempts: Array<{call_id:string;state:Stage["state"] | "retried"}>;
  explanations: string[]; context_counts: { deploys: number; anomalies: number }; saved?: DashboardToolResult;
};
const contextTools = new Set(["get_intelligence_snapshot", "query_telemetry", "get_observability_overview", "get_service_topology", "get_service_dependencies", "get_service_performance", "inspect_trace", "search_logs"]);
const namedProblems = (value: unknown, id: string): Correction[] => Array.isArray(value) ? value.flatMap(p => object(p) && typeof p.path === "string" && typeof p.message === "string" ? [{panel_id:id,path:p.path,message:p.message}] : []) : [];
export const interruptedResult = (message:Message) => message.role === "tool" && (message.error === "interrupted" || jsonObject(message.content)?.error === "interrupted");
const executed = (p: PanelCheck) => !["invalid", "not_run"].includes(p.status);

/** No browser-only progress state: calls, inputs and server results in this user
 * turn are sufficient to reconstruct exactly the same receipt after reload. */
export function receiptForTurn(messages: readonly Message[], turnID: string): BuildReceipt | null {
  const start = messages.findIndex(m => m.id === turnID && m.role === "user");
  if (start < 0) return null;
  const next = messages.findIndex((m, i) => i > start && m.role === "user");
  const turn = messages.slice(start + 1, next < 0 ? undefined : next);
  const refused = new Set(turn.flatMap(m => m.role === "tool" && jsonObject(m.content)?.code === "answer_only" ? [m.toolCallId] : []));
  const calls = turn.flatMap((m, i) => m.role === "assistant" ? (m.toolCalls ?? []).map(call => ({call,index:i})) : []).filter(({call}) => !refused.has(call.id));
  if (!calls.some(({call}) => mutationNames.has(call.function.name) || call.function.name === "preview_panels")) return null;
  const stages: BuildReceipt["stages"] = {schema:{state:"unobserved"},context:{state:"unobserved"},draft:{state:"unobserved"},validation:{state:"unobserved"},preview:{state:"unobserved"},save:{state:"unobserved"}};
  const receipt: BuildReceipt = {turn_id:turnID,stages,panels:[],problems:[],corrections:[],save_attempts:[],explanations:[],context_counts:{deploys:0,anomalies:0}};
  const callCounts = new Map<string, number>(), toolResults = new Map<string, Array<{index:number;message:Message}>>();
  for (const {call} of calls) callCounts.set(call.id,(callCounts.get(call.id) ?? 0)+1);
  turn.forEach((message,index) => {if(message.role === "tool") {const results=toolResults.get(message.toolCallId) ?? [];results.push({index,message});toolResults.set(message.toolCallId,results);}});
  const netChanges = new Map<string,Change>();
  let firstSave: DashboardToolResult | undefined, unreadableSave = false, lastMutationError = false;
  const clearProblems = (id:string,issues:Correction[]) => {
    for(const problem of resolvedProblems(outstanding.get(id) ?? [],issues,new Set([id]))) {
      if(!receipt.corrections.some(c=>c.panel_id===problem.panel_id && c.path===problem.path)) receipt.corrections.push(problem);
    }
    outstanding.set(id,issues);
  };
  const panels = new Map<string, PanelCheck>(), outstanding = new Map<string, Correction[]>(), previewFailures = new Set<string>();
  let sawPreview = false, unmatchedPreview = false;
  for (const {call,index} of calls) {
    // Evaluate in call order, so out-of-order parallel results cannot turn an
    // older preview into a later correction. Ignore duplicate/ambiguous IDs.
    if (callCounts.get(call.id) !== 1) continue;
    const name = call.function.name, input = jsonObject(call.function.arguments);
    const results = (toolResults.get(call.id) ?? []).filter(r => r.index > index);
    // A synthetic interruption is uncertainty, so a later definitive result
    // for that call can replace it. Other duplicate results stay ambiguous.
    const result = results.length === 1 || mutationNames.has(name) && results.length > 1 && results.slice(0,-1).every(r => interruptedResult(r.message)) ? results.at(-1)!.message : undefined;
    const payload = result && jsonObject(result.content);
    const success = !!payload && result?.role === "tool" && !result.error && !payload.error && !payload.isError;
    const state = !result ? "incomplete" : success ? "complete" : "failed";
    if (name === "get_telemetry_schema") stages.schema = {state};
    if (contextTools.has(name)) {
      const contextSuccess = result?.role === "tool" && !result.error && !payload?.error && !payload?.isError && typeof result.content === "string" && result.content.length > 0;
      stages.context = {state: !result ? "incomplete" : contextSuccess ? "complete" : "failed"};
      if (success) for (const [key, fields] of [["deploys", ["deploys", "deployments"]], ["anomalies", ["anomalies"]]] as const) {
        for (const field of fields) if (Array.isArray(payload[field])) receipt.context_counts[key] += payload[field].length;
      }
    }
    if (name === "preview_panels" || name === "create_dashboard" || name === "replace_dashboard") {
      const draft = name === "preview_panels" ? input?.panels : object(input?.dashboard) ? input.dashboard.panels : undefined;
      if (Array.isArray(draft)) { receipt.panel_count = draft.length; stages.draft = {state:"complete"}; }
    }
    if (name === "preview_panels") {
      sawPreview = true;
      const expected = Array.isArray(input?.panels) ? input.panels.flatMap(p => object(p) && typeof p.id === "string" ? [p.id] : []) : [];
      for (const id of expected) panels.set(id,{id,status:"not_run"});
      const checked = success && Array.isArray(payload.panels) && payload.panels.every(panelCheck) ? payload.panels : undefined;
      const elapsed = typeof payload?.elapsed_ms === "number" && payload.elapsed_ms >= 0 ? payload.elapsed_ms : undefined;
      stages.validation = {state:!checked ? state === "complete" ? "failed" : state : checked.some(p => p.status === "invalid" || p.status === "not_run") || namedProblems(payload?.problems, "dashboard").length ? "failed" : "complete"};
      stages.preview = {state:checked ? checked.every(executed) ? "complete" : "incomplete" : state === "complete" ? "failed" : state,elapsed_ms:elapsed};
      if (!checked || expected.length === 0 || checked.length !== expected.length || new Set(checked.map(p => p.id)).size !== expected.length || checked.some(p => !expected.includes(p.id))) {
        unmatchedPreview = expected.length === 0;
        for (const id of expected) { if (result) previewFailures.add(id); }
        continue;
      }
      unmatchedPreview = false;
      for (const id of expected) previewFailures.delete(id);
      for (const p of checked) {
        const issues = namedProblems((p as unknown as Record<string, unknown>).problems, p.id);
        if (p.status !== "not_run") clearProblems(p.id,issues);
        else {
          const priorIssues = outstanding.get(p.id) ?? [];
          outstanding.set(p.id,[...priorIssues,...issues.filter(issue=>!priorIssues.some(old=>old.path===issue.path))]);
        }
        panels.set(p.id,p);
      }
      receipt.problems = [...outstanding.values()].flat().concat(namedProblems(payload?.problems,"dashboard"));
    }
    if (mutationNames.has(name)) {
      const saved = result && dashboardToolResult(call.id,result.content,messages);
      const interrupted = !!result && interruptedResult(result);
      const error = !interrupted && result?.role === "tool" && (!!result.error || !!payload?.error || !!payload?.isError);
      lastMutationError = !!error;
      unreadableSave = !!result && !saved && !error && !interrupted;
      stages.save = {state:saved ? "complete" : interrupted ? "interrupted" : error ? "failed" : "incomplete"};
      if(saved) for(const attempt of receipt.save_attempts) if(attempt.state === "failed") attempt.state = "retried";
      receipt.save_attempts.push({call_id:call.id,state:stages.save.state});
      if (saved) {
        if(firstSave?.id !== saved.id) {firstSave=saved;netChanges.clear();}
        for(const change of saved.receipt.changes) {
          const prior=netChanges.get(change.panel_id);
          if(prior?.kind === "added" && change.kind === "removed") {netChanges.delete(change.panel_id);continue;}
          if(prior?.kind === "added") {netChanges.set(change.panel_id,{panel_id:change.panel_id,title:change.title,kind:"added"});continue;}
          if(change.kind === "removed") {netChanges.set(change.panel_id,change);continue;}
          if(prior && change.kind === "changed") {
            netChanges.set(change.panel_id,{...change,fields:[...new Set([...(prior.fields ?? []),...(change.fields ?? [])])].sort(),position_changed:prior.position_changed || change.position_changed});
          } else netChanges.set(change.panel_id,prior?.kind === "removed" && change.kind === "added" ? {...change,kind:"changed"} : change);
        }
        const previous=receipt.saved?.id === saved.id ? receipt.saved.receipt : undefined;
        receipt.saved = {...saved,label:firstSave!.label,receipt:{...saved.receipt,base_version:firstSave!.receipt.base_version,changes:[...netChanges.values()],layout_changed:firstSave!.receipt.base_version !== 0 && (saved.receipt.layout_changed || !!previous?.layout_changed),dashboard_fields:firstSave!.receipt.base_version === 0 ? undefined : [...new Set([...(previous?.dashboard_fields ?? []),...(saved.receipt.dashboard_fields ?? [])])].sort()}};
        receipt.panel_count = saved.receipt.save_check.panels.length;
        stages.draft = {state:"complete"};
        const retained = new Set(saved.receipt.save_check.panels.map(p => p.id));
        for(const p of saved.receipt.save_check.panels) if(executed(p)) {
          clearProblems(p.id,[]);
          previewFailures.delete(p.id);
        }
        receipt.corrections = receipt.corrections.filter(p => retained.has(p.panel_id));
        receipt.problems = [...outstanding.values()].flat().filter(p=>retained.has(p.panel_id));
      }
    }
  }
  if(unreadableSave) return null;
  const retained = receipt.saved ? new Set(receipt.saved.receipt.save_check.panels.map(p => p.id)) : undefined;
  receipt.panels = [...panels.values()].filter(p => !retained || retained.has(p.id));
  if (sawPreview) {
    const failed = unmatchedPreview || [...previewFailures].some(id => !retained || retained.has(id));
    const incomplete = receipt.panels.some(p => p.status === "not_run");
    const invalid = receipt.problems.length > 0;
    stages.validation.state = failed || invalid ? "failed" : incomplete ? "incomplete" : "complete";
    stages.preview.state = failed ? "failed" : incomplete || invalid || receipt.panels.some(p => p.status === "invalid") ? "incomplete" : "complete";
  }
  const check = receipt.saved?.receipt.save_check;
  if (check && !check.checked) receipt.explanations.push(`Save check incomplete: ${check.reason || "Panels were not checked"}`);
  for (const p of check?.panels ?? receipt.panels) if (p.status !== "ok") receipt.explanations.push(`${p.id}: ${p.status}${p.error || p.diagnosis ? ` — ${p.error || p.diagnosis}` : ""}`);
  for (const p of receipt.problems) receipt.explanations.push(`${p.panel_id}.${p.path}: ${p.message}`);
  const unfinished = Object.entries(stages).filter(([,s]) => s.state === "incomplete" || s.state === "failed").map(([name,s]) => `${name}: ${s.state}`);
  if (unfinished.length) receipt.explanations.push(unfinished.join(" · "));
  if (stages.save.state === "interrupted") receipt.explanations.push("Save interrupted · outcome unknown");
  if (lastMutationError) receipt.explanations.push(receipt.saved ? `A later change failed; v${receipt.saved.version} remains saved` : "Save failed · dashboard was not saved");
  return receipt;
}
