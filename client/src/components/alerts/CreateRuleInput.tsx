import { useState } from "react";
import { Sparkles } from "lucide-react";
import { api } from "@/api/client";
import type { AlertRule } from "@/lib/types";

interface Props {
  onCreated: () => void;
}

export function CreateRuleInput({ onCreated }: Props) {
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [saving, setSaving] = useState(false);

  // Advanced form state
  const [name, setName] = useState("");
  const [expression, setExpression] = useState("");
  const [service, setService] = useState("*");
  const [forSeconds, setForSeconds] = useState(0);
  const [cooldownS, setCooldownS] = useState(300);
  const [repeatS, setRepeatS] = useState(3600);
  const [webhookUrl, setWebhookUrl] = useState("");
  const [notifyResolve, setNotifyResolve] = useState(false);

  async function saveRule() {
    if (!name.trim() || !expression.trim()) return;
    setSaving(true);
    try {
      await api<AlertRule>("/api/alert-rules", {
        method: "POST",
        body: JSON.stringify({
          name,
          expression,
          service: service || "*",
          enabled: true,
          for_seconds: forSeconds,
          cooldown_s: cooldownS,
          repeat_interval_s: repeatS,
          webhook_url: webhookUrl,
          notify_on_resolve: notifyResolve,
        }),
      });
      // Reset form
      setName("");
      setExpression("");
      setService("*");
      setForSeconds(0);
      setWebhookUrl("");
      setShowAdvanced(false);
      onCreated();
    } catch (err) {
      console.error("Create rule failed:", err);
    } finally {
      setSaving(false);
    }
  }

  if (!showAdvanced) {
    return (
      <button
        type="button"
        onClick={() => setShowAdvanced(true)}
        className="w-full rounded-lg border border-dashed border-border/60 bg-surface-1/40 px-4 py-3 text-left text-xs text-muted-foreground hover:border-primary/30 hover:text-foreground transition-colors mono flex items-center gap-2"
      >
        <Sparkles className="h-3.5 w-3.5 text-primary/50" />
        New alert rule...
      </button>
    );
  }

  return (
    <div className="rounded-xl border border-primary/15 bg-surface-1/80 p-4 space-y-3 fade-up">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div>
          <label className="detail-label mb-1 block">Name</label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="high_error_rate"
            className="input-field"
          />
        </div>
        <div>
          <label className="detail-label mb-1 block">Service</label>
          <input
            type="text"
            value={service}
            onChange={(e) => setService(e.target.value)}
            placeholder="* (all services)"
            className="input-field"
          />
        </div>
      </div>

      <div>
        <label className="detail-label mb-1 block">Expression</label>
        <input
          type="text"
          value={expression}
          onChange={(e) => setExpression(e.target.value)}
          placeholder="error_rate > 0.05"
          className="input-field mono"
        />
        <div className="mt-1 text-[10px] text-muted-foreground/60 mono">
          error_rate · p50 · p95 · throughput · log_count · z_score · health_score · error_rate_delta · p95_delta · throughput_delta
        </div>
      </div>

      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <div>
          <label className="detail-label mb-1 block">For (sec)</label>
          <input
            type="number"
            value={forSeconds}
            onChange={(e) => setForSeconds(Number(e.target.value))}
            min={0}
            className="input-field"
          />
        </div>
        <div>
          <label className="detail-label mb-1 block">Cooldown (sec)</label>
          <input
            type="number"
            value={cooldownS}
            onChange={(e) => setCooldownS(Number(e.target.value))}
            min={0}
            className="input-field"
          />
        </div>
        <div>
          <label className="detail-label mb-1 block">Repeat (sec)</label>
          <input
            type="number"
            value={repeatS}
            onChange={(e) => setRepeatS(Number(e.target.value))}
            min={0}
            className="input-field"
          />
        </div>
        <div className="flex items-end pb-1">
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={notifyResolve}
              onChange={(e) => setNotifyResolve(e.target.checked)}
              className="accent-primary"
            />
            <span className="text-xs text-muted-foreground">Notify resolve</span>
          </label>
        </div>
      </div>

      <div>
        <label className="detail-label mb-1 block">Webhook URL</label>
        <input
          type="text"
          value={webhookUrl}
          onChange={(e) => setWebhookUrl(e.target.value)}
          placeholder="https://hooks.slack.com/services/..."
          className="input-field"
        />
      </div>

      <div className="flex gap-2">
        <button
          type="button"
          onClick={saveRule}
          disabled={saving || !name.trim() || !expression.trim()}
          className="btn-primary text-xs disabled:opacity-50"
        >
          {saving ? "Saving..." : "Save Rule"}
        </button>
        <button
          type="button"
          onClick={() => setShowAdvanced(false)}
          className="btn-ghost text-xs"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}
