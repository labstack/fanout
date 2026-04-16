import { useRef, useState } from "react";
import { useLocation } from "react-router";
import { Sparkles, Settings, Loader2 } from "lucide-react";
import { api } from "@/api/client";
import { getApiToken } from "@/api/client";
import type { AlertRule, ChatEvent } from "@/lib/types";

interface Props {
  onCreated: () => void;
}

export function CreateRuleInput({ onCreated }: Props) {
  const { search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? undefined;

  const [input, setInput] = useState("");
  const [showManual, setShowManual] = useState(false);
  const [saving, setSaving] = useState(false);
  const [aiStatus, setAiStatus] = useState<string | null>(null);
  const [aiError, setAiError] = useState<string | null>(null);
  const [streaming, setStreaming] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  // Manual form state
  const [name, setName] = useState("");
  const [expression, setExpression] = useState("");
  const [service, setService] = useState("*");
  const [forSeconds, setForSeconds] = useState(0);
  const [cooldownS, setCooldownS] = useState(300);
  const [repeatS, setRepeatS] = useState(3600);
  const [webhookUrl, setWebhookUrl] = useState("");
  const [notifyResolve, setNotifyResolve] = useState(false);

  async function handleAICreate() {
    if (!input.trim() || streaming) return;

    setStreaming(true);
    setAiStatus("Creating alert rule...");
    setAiError(null);
    abortRef.current = new AbortController();

    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
      };
      const authToken = token || getApiToken();
      if (authToken) headers["Authorization"] = `Bearer ${authToken}`;

      const resp = await fetch("/api/chat", {
        method: "POST",
        headers,
        body: JSON.stringify({
          content: `Create an alert rule: ${input.trim()}`,
        }),
        signal: abortRef.current.signal,
      });

      if (!resp.ok) {
        setAiError(`Failed: HTTP ${resp.status}`);
        setStreaming(false);
        setAiStatus(null);
        return;
      }

      const reader = resp.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let resultText = "";
      let ruleCreated = false;

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";

        let currentEvent = "";
        for (const line of lines) {
          if (line.startsWith("event: ")) {
            currentEvent = line.slice(7);
          } else if (line.startsWith("data: ") && currentEvent) {
            try {
              const data = JSON.parse(line.slice(6)) as ChatEvent;
              if (currentEvent === "tool_call" && data.name === "alert_rules") {
                setAiStatus("Creating rule...");
              }
              if (currentEvent === "tool_result" && data.name === "alert_rules") {
                ruleCreated = true;
                setAiStatus("Rule created!");
              }
              if (currentEvent === "token" && data.content) {
                resultText += data.content;
              }
              if (currentEvent === "error" && data.error) {
                setAiError(data.error);
              }
            } catch {
              // skip malformed SSE
            }
            currentEvent = "";
          }
        }
      }

      if (ruleCreated) {
        setInput("");
        onCreated();
        // Clear status after a moment
        setTimeout(() => setAiStatus(null), 2000);
      } else if (!aiError) {
        setAiStatus(resultText ? "Done — check rules below" : null);
        onCreated(); // refresh anyway in case rule was created
      }
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") {
        setAiStatus(null);
      } else {
        setAiError("Failed to create rule");
        console.error("AI alert creation failed:", err);
      }
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
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
            disabled={streaming}
          />
        </div>
        <button
          type="button"
          onClick={handleAICreate}
          disabled={!input.trim() || streaming}
          className="btn-primary text-xs disabled:opacity-50 shrink-0"
        >
          {streaming ? (
            <span className="flex items-center gap-1.5">
              <Loader2 className="h-3 w-3 animate-spin" />
              Creating
            </span>
          ) : (
            "Create"
          )}
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

      {/* AI status/error */}
      {aiStatus && (
        <div className="text-[11px] mono text-primary/70 px-1">
          {aiStatus}
        </div>
      )}
      {aiError && (
        <div className="text-[11px] mono text-unhealthy/80 px-1">
          {aiError}
        </div>
      )}

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
