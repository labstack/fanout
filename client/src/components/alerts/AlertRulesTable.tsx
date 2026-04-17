import type { AlertRule, AlertInstance } from "@/lib/types";
import { api } from "@/api/client";

interface Props {
  rules: AlertRule[];
  alerts: AlertInstance[];
  onRefresh: () => void;
}

export function AlertRulesTable({ rules, alerts, onRefresh }: Props) {
  if (rules.length === 0) {
    return (
      <div className="rounded-lg border border-border/60 bg-surface-1/80 px-4 py-6 text-center text-sm text-muted-foreground">
        No alert rules configured. Create one above.
      </div>
    );
  }

  // Count firing alerts per rule
  const firingByRule = new Map<string, number>();
  for (const a of alerts) {
    if (a.state === "firing") {
      firingByRule.set(a.rule_id, (firingByRule.get(a.rule_id) || 0) + 1);
    }
  }

  async function toggleEnabled(rule: AlertRule) {
    try {
      await api(`/api/rules/${rule.id}`, {
        method: "PUT",
        body: JSON.stringify({ ...rule, enabled: !rule.enabled }),
      });
      onRefresh();
    } catch (err) {
      console.error("Toggle rule failed:", err);
    }
  }

  async function deleteRule(id: string) {
    try {
      await api(`/api/rules/${id}`, { method: "DELETE" });
      onRefresh();
    } catch (err) {
      console.error("Delete rule failed:", err);
    }
  }

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr>
              <th className="text-left p-3 w-10"></th>
              <th className="text-left p-3">Name</th>
              <th className="text-left p-3">Expression</th>
              <th className="text-left p-3">Service</th>
              <th className="text-left p-3">Webhook</th>
              <th className="text-right p-3">Status</th>
              <th className="text-right p-3 w-16"></th>
            </tr>
          </thead>
          <tbody>
            {rules.map((rule) => {
              const firingCount = firingByRule.get(rule.id) || 0;
              return (
                <tr key={rule.id} className="border-t border-border/30 hover:bg-surface-2/50 transition-colors">
                  <td className="p-3">
                    <button
                      type="button"
                      onClick={() => toggleEnabled(rule)}
                      className={`relative w-8 h-[18px] rounded-full transition-colors cursor-pointer ${
                        rule.enabled ? "bg-healthy/30" : "bg-surface-3"
                      }`}
                      title={rule.enabled ? "Disable" : "Enable"}
                    >
                      <span
                        className={`absolute top-[3px] h-3 w-3 rounded-full transition-all ${
                          rule.enabled ? "left-[17px] bg-healthy" : "left-[3px] bg-muted-foreground/50"
                        }`}
                      />
                    </button>
                  </td>
                  <td className="p-3 text-sm text-foreground">{rule.name}</td>
                  <td className="p-3 text-xs text-primary mono truncate max-w-[240px]" title={rule.expression}>
                    {rule.expression}
                  </td>
                  <td className="p-3 text-xs text-muted-foreground mono">
                    {rule.service || "*"}
                  </td>
                  <td className="p-3 text-xs text-muted-foreground mono truncate max-w-[140px]">
                    {rule.webhook_url ? (() => { try { return new URL(rule.webhook_url).hostname; } catch { return rule.webhook_url; } })() : "none"}
                  </td>
                  <td className="p-3 text-right">
                    {!rule.enabled ? (
                      <span className="text-[10px] text-muted-foreground">disabled</span>
                    ) : firingCount > 0 ? (
                      <span className="text-[10px] text-unhealthy">
                        firing{firingCount > 1 ? ` (${firingCount})` : ""}
                      </span>
                    ) : (
                      <span className="text-[10px] text-healthy">ok</span>
                    )}
                  </td>
                  <td className="p-3 text-right">
                    <button
                      type="button"
                      onClick={() => deleteRule(rule.id)}
                      className="text-[10px] text-muted-foreground hover:text-unhealthy transition-colors mono"
                    >
                      delete
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
