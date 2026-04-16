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

function fmtValue(v?: number): string {
  if (v === undefined || v === 0) return "";
  if (Number.isInteger(v)) return v.toLocaleString();
  return v.toPrecision(3);
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
        const prompt = `Investigate ${a.service} — alert "${rule?.name || a.rule_id}" is firing. Expression: ${rule?.expression}. What's the root cause?`;

        const status = a.last_delivery_status;
        const deliveryCls = status === "success"
          ? "border-healthy/20 bg-healthy/10 text-healthy"
          : status === "failed"
            ? "border-unhealthy/20 bg-unhealthy/10 text-unhealthy"
            : "border-border bg-surface-2 text-muted-foreground";
        const deliveryLabel = !status || status === "skipped"
          ? "no webhook"
          : status === "success"
            ? "webhook \u2713"
            : "webhook \u2717";

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
                <span className={`inline-flex rounded-full border px-2 py-0.5 text-[9px] font-bold ${deliveryCls}`}>
                  {deliveryLabel}
                </span>
                <button
                  type="button"
                  onClick={() => navigate(buildChatPath(prompt, token))}
                  className="btn-ghost text-xs"
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
