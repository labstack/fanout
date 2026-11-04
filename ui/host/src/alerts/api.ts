import { request } from "../dashboards/api";

/** internal/alert/types.go Rule; a replace sends the complete rule, as omitted fields become zero values. */
export type Rule = {
  id: string; name: string; description?: string; enabled: boolean; service?: string; namespace?: string; expression: string;
  for_seconds: number; cooldown_s: number; repeat_interval_s: number;
  webhook_url?: string; webhook_headers?: string; webhook_template?: string; notify_on_resolve: boolean;
  created_at: string; updated_at: string;
};
export type RuleDraft = Omit<Rule, "id" | "created_at" | "updated_at">;
export type Alert = {
  id: string; rule_id: string; service: string; state: "pending" | "firing" | "resolved" | string; value?: number;
  fired_at?: string; resolved_at?: string; last_evaluated_at?: string; last_delivery_status?: string; last_delivery_at?: string; created_at: string;
};
/** One service's dry run; `env` holds the measured values by variable name, and `no_data` marks a service without recent telemetry. */
export type RuleTest = { results: { service: string; triggered: boolean; error?: string; no_data?: boolean; env?: Record<string, number | string> }[] | null };

export const rulesKey = ["alerting", "rules"] as const;
export const alertsKey = ["alerting", "alerts"] as const;
const rulePath = (id: string) => `/api/alerting/rules/${encodeURIComponent(id)}`;

export const listRules = (signal?: AbortSignal) => request<{ rules: Rule[] }>("/api/alerting/rules", { signal }).then(r => r.rules);
export const listAlerts = (signal?: AbortSignal) => request<{ alerts: Alert[] }>("/api/alerting/alerts", { signal }).then(r => r.alerts);
export const createRule = (rule: RuleDraft) => request<Rule>("/api/alerting/rules", { method: "POST", json: rule });
export const replaceRule = (id: string, rule: RuleDraft) => request<Rule>(rulePath(id), { method: "PUT", json: rule });
export const deleteRule = (id: string) => request<unknown>(rulePath(id), { method: "DELETE" });
/** Services with telemetry in the last hour. */
export const listServices = (signal?: AbortSignal) => request<{ services?: { value: string }[] }>("/api/telemetry/schema?window=1h", { signal }).then(r => (r.services ?? []).map(s => s.value));
export const testRule = (id: string) => request<RuleTest>(`${rulePath(id)}/test`, { method: "POST" });
