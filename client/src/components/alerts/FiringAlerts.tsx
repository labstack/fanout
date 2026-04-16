import { useNavigate, useLocation } from "react-router";
import type { AlertInstance, AlertRule } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";

function timeAgo(iso?: string): string {
  if (!iso) return "";
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  return `${hrs}h`;
}

function deliveryBadge(status?: string) {
  if (!status || status === "skipped")
    return { label: "no webhook", cls: "border-border bg-surface-2 text-muted-foreground" };
  if (status === "success")
    return { label: "webhook \u2713", cls: "border-healthy/20 bg-healthy/10 text-healthy" };
  return { label: "webhook \u2717", cls: "border-unhealthy/20 bg-unhealthy/10 text-unhealthy" };
}

function fmtValue(v?: number): string {
  if (v === undefined) return "";
  if (v < 1) return `${(v * 100).toFixed(1)}%`;
  if (v >= 1000) return `${(v / 1000).toFixed(1)}s`;
  return `${v.toFixed(0)}ms`;
}

interface Props {
  alerts: AlertInstance[];
  rules: AlertRule[];
}

export function FiringAlerts({ alerts, rules }: Props) {
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? undefined;

  const firing = alerts.filter((a) => a.state === "firing");
  if (firing.length === 0) {
    return (
      <div className="rounded-lg border border-healthy/15 bg-healthy/5 px-4 py-3 text-sm text-healthy/90 mono">
        No alerts firing
      </div>
    );
  }

  const ruleMap = new Map(rules.map((r) => [r.id, r]));

  return (
    <div className="space-y-2">
      {firing.map((a) => {
        const rule = ruleMap.get(a.rule_id);
        const badge = deliveryBadge(a.last_delivery_status);
        const prompt = `Investigate ${a.service} — alert "${rule?.name || a.rule_id}" is firing. Expression: ${rule?.expression}. Current value: ${fmtValue(a.value)}. What's the root cause?`;

        return (
          <div
            key={a.id}
            className="rounded-xl border border-unhealthy/15 bg-unhealthy/5 p-4"
          >
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 mb-2">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-unhealthy text-sm">{"\u2716"}</span>
                <button
                  type="button"
                  onClick={() => navigate(`/service/${encodeURIComponent(a.service)}${search}`)}
                  className="font-heading text-sm font-semibold text-foreground hover:text-primary transition-colors"
                >
                  {a.service}
                </button>
                <span className="text-[11px] text-muted-foreground mono">
                  {rule?.name || a.rule_id}
                </span>
                {a.fired_at && (
                  <span className="text-[11px] text-muted-foreground mono">
                    firing for {timeAgo(a.fired_at)}
                  </span>
                )}
              </div>
              <div className="flex items-center gap-2">
                <span className={`inline-flex rounded border px-2 py-0.5 text-[9px] font-bold ${badge.cls}`}>
                  {badge.label}
                </span>
                <button
                  type="button"
                  onClick={() => navigate(buildChatPath(prompt, token))}
                  className="btn-ghost text-xs px-3 py-1.5"
                >
                  Investigate
                </button>
              </div>
            </div>
            <div className="flex flex-wrap gap-3 text-[11px] text-muted-foreground mono">
              {rule && <span>expr: <span className="text-foreground/80">{rule.expression}</span></span>}
              {a.value !== undefined && <span>value: <span className="text-unhealthy">{fmtValue(a.value)}</span></span>}
              {a.fired_at && <span>fired: {new Date(a.fired_at).toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false })}</span>}
              {a.last_delivery_at && (
                <span>
                  webhook: {timeAgo(a.last_delivery_at)} ago {a.last_delivery_status === "success" ? "\u2713" : "\u2717"}
                </span>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
