import { useState } from "react";
import { useNavigate, useLocation } from "react-router";
import { Sparkles, Settings } from "lucide-react";
import { api } from "@/api/client";
import { buildChatPath } from "@/lib/chat-route";
import type { AlertRule } from "@/lib/types";

interface Props {
  onCreated: () => void;
}

export function CreateRuleInput({ onCreated }: Props) {
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? undefined;

  const [input, setInput] = useState("");
  const [showManual, setShowManual] = useState(false);
  const [saving, setSaving] = useState(false);

  // Manual form state
  const [name, setName] = useState("");
  const [expression, setExpression] = useState("");
  const [service, setService] = useState("*");
  const [forSeconds, setForSeconds] = useState(0);
  const [cooldownS, setCooldownS] = useState(300);
  const [repeatS, setRepeatS] = useState(3600);
  const [webhookUrl, setWebhookUrl] = useState("");
  const [notifyResolve, setNotifyResolve] = useState(false);

  function handleAICreate() {
    if (!input.trim()) return;
    const prompt = `Create an alert rule: ${input.trim()}`;
    navigate(buildChatPath(prompt, token));
  }

  async function saveManualRule() {
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
      setName("");
      setExpression("");
      setService("*");
      setForSeconds(0);
      setWebhookUrl("");
      setInput("");
      setShowManual(false);
      onCreated();
    } catch (err) {
      console.error("Create rule failed:", err);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="space-y-2">
      {/* AI creation — primary path */}
      <div className="flex gap-2">
        <div className="relative flex-1">
          <Sparkles className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-primary/40" />
          <input
            type="text"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && handleAICreate()}
            placeholder="Alert me when checkout error rate exceeds 5% for 2 minutes..."
            className="input-field pl-9"
          />
        </div>
        <button
          type="button"
          onClick={handleAICreate}
          disabled={!input.trim()}
          className="btn-primary text-xs disabled:opacity-50 shrink-0"
        >
          Create
        </button>
        <button
          type="button"
          onClick={() => setShowManual(!showManual)}
          className="btn-ghost text-xs shrink-0"
          title="Manual form"
        >
          <Settings className="h-3.5 w-3.5" />
        </button>
      </div>

      {/* Manual form — advanced fallback */}
      {showManual && (
        <div className="rounded-xl border border-border/60 bg-surface-1/80 p-4 space-y-3 fade-up">
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
              onClick={saveManualRule}
              disabled={saving || !name.trim() || !expression.trim()}
              className="btn-primary text-xs disabled:opacity-50"
            >
              {saving ? "Saving..." : "Save Rule"}
            </button>
            <button
              type="button"
              onClick={() => setShowManual(false)}
              className="btn-ghost text-xs"
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
