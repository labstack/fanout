import type { DashboardSpec, DashboardTime, VarValue } from "../../../panels/types";
import { panelContent } from "../../../panels/content";

// Shared by the live hooks and cache consumers; layout stays out of these keys.
export function panelResultsKey(id: string, spec: DashboardSpec, time: DashboardTime, vars: Record<string, VarValue>, compare: boolean) {
  return ["panels", JSON.stringify([id, panelContent(spec), time, vars, compare])] as const;
}
export function variableOptionsKey(scope: string, spec: DashboardSpec, time: DashboardTime, vars: Record<string, VarValue>) {
  return ["variables", scope, panelContent(spec), time, vars] as const;
}
