import { useRef, useState } from "react";
import { useLocation } from "react-router";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Sparkles, Loader2, Check, Trash2, Settings } from "lucide-react";
import { api, getApiToken } from "@/api/client";
import type { Rule, ChatEvent } from "@/lib/types";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";

interface Props {
  onCreated: () => void;
}

interface CreatedRule {
  name: string;
  expression: string;
  service: string;
  for_seconds: number;
}

const ruleSchema = z.object({
  name: z.string().min(1, "Name is required"),
  expression: z.string().min(1, "Expression is required"),
  service: z.string(),
  forSeconds: z.coerce.number().int().min(0),
  cooldownS: z.coerce.number().int().min(0),
  repeatS: z.coerce.number().int().min(0),
  webhookUrl: z.string(),
  notifyResolve: z.boolean(),
});

type RuleInput = z.input<typeof ruleSchema>;
type RuleValues = z.output<typeof ruleSchema>;

export function CreateRuleInput({ onCreated }: Props) {
  const { search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? undefined;

  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [aiStatus, setAiStatus] = useState<string | null>(null);
  const [aiError, setAiError] = useState<string | null>(null);
  const [createdRule, setCreatedRule] = useState<CreatedRule | null>(null);
  const [showManual, setShowManual] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const sessionIdRef = useRef(crypto.randomUUID());

  const form = useForm<RuleInput, unknown, RuleValues>({
    resolver: zodResolver(ruleSchema),
    defaultValues: {
      name: "",
      expression: "",
      service: "*",
      forSeconds: 0,
      cooldownS: 300,
      repeatS: 3600,
      webhookUrl: "",
      notifyResolve: false,
    },
  });

  async function handleAICreate() {
    if (!input.trim() || streaming) return;

    setStreaming(true);
    setAiStatus("Thinking...");
    setAiError(null);
    setCreatedRule(null);
    abortRef.current = new AbortController();

    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
        "X-Session-ID": sessionIdRef.current,
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

      if (!resp.body) {
        setStreaming(false);
        setAiStatus(null);
        return;
      }
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";

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
                if (data.input) {
                  try {
                    const toolInput = JSON.parse(data.input);
                    if (toolInput.action === "create" && toolInput.name) {
                      setCreatedRule({
                        name: toolInput.name,
                        expression: toolInput.expression || "",
                        service: toolInput.service || "*",
                        for_seconds: toolInput.for_seconds || 0,
                      });
                    }
                  } catch {
                    /* partial JSON from streaming — expected */
                  }
                }
              }
              if (
                currentEvent === "tool_result" &&
                data.name === "alert_rules"
              ) {
                setAiStatus(null);
                onCreated();
              }
              if (currentEvent === "error" && data.error) {
                setAiError(data.error);
              }
            } catch (e) {
              console.warn("[AlertCreate] malformed SSE data:", e);
            }
            currentEvent = "";
          }
        }
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
      if (!aiError) setAiStatus(null);
      abortRef.current = null;
    }
  }

  function dismissCreated() {
    setCreatedRule(null);
    setInput("");
  }

  async function saveManualRule(values: RuleValues) {
    try {
      await api<Rule>("/api/rules", {
        method: "POST",
        body: JSON.stringify({
          name: values.name,
          expression: values.expression,
          service: values.service || "*",
          enabled: true,
          for_seconds: values.forSeconds,
          cooldown_s: values.cooldownS,
          repeat_interval_s: values.repeatS,
          webhook_url: values.webhookUrl,
          notify_on_resolve: values.notifyResolve,
        }),
      });
      form.reset();
      setShowManual(false);
      onCreated();
    } catch (err) {
      console.error("Create rule failed:", err);
      setAiError(err instanceof Error ? err.message : "Failed to save rule");
    }
  }

  return (
    <div className="space-y-2">
      <div className="relative">
        {streaming ? (
          <Loader2 className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 animate-spin text-primary" />
        ) : (
          <Sparkles className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-primary/40" />
        )}
        <Input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && handleAICreate()}
          placeholder="Alert me when checkout error rate exceeds 5% for 2 minutes..."
          className="pl-9 pr-20"
          disabled={streaming}
        />
        <div className="absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1">
          {input.trim() && !streaming && (
            <span className="mr-1 font-mono text-[10px] text-muted-foreground/40">
              enter
            </span>
          )}
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={() => setShowManual(!showManual)}
            className="size-7 text-muted-foreground/40 hover:text-foreground"
            aria-label="Toggle manual form"
          >
            <Settings className="size-3.5" />
          </Button>
        </div>
      </div>

      {aiStatus && (
        <div className="flex items-center gap-1.5 px-1 font-mono text-[11px] text-primary/70">
          <Loader2 className="size-3 animate-spin" />
          {aiStatus}
        </div>
      )}
      {aiError && (
        <div className="px-1 font-mono text-[11px] text-danger/80">
          {aiError}
        </div>
      )}

      {createdRule && !streaming && (
        <div className="fade-up rounded-lg border border-primary/15 bg-primary/5 p-3">
          <div className="flex items-start justify-between gap-3">
            <div>
              <div className="mb-1 flex items-center gap-2">
                <Check className="size-3.5 text-success" />
                <span className="font-heading text-sm font-semibold text-foreground">
                  {createdRule.name}
                </span>
              </div>
              <div className="flex items-center gap-3 font-mono text-[11px] text-muted-foreground">
                <span className="text-primary">{createdRule.expression}</span>
                <span>{createdRule.service}</span>
                {createdRule.for_seconds > 0 && (
                  <span>for {createdRule.for_seconds}s</span>
                )}
              </div>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={dismissCreated}
              className="size-7 text-muted-foreground/40 hover:text-foreground"
              aria-label="Dismiss"
            >
              <Trash2 className="size-3.5" />
            </Button>
          </div>
        </div>
      )}

      {showManual && (
        <Form {...form}>
          <form
            onSubmit={form.handleSubmit(saveManualRule)}
            className="fade-up space-y-3 rounded-xl border border-border/60 bg-surface-1/80 p-4"
          >
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <FormField
                control={form.control}
                name="name"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel className="detail-label">Name</FormLabel>
                    <FormControl>
                      <Input placeholder="high_error_rate" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="service"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel className="detail-label">Service</FormLabel>
                    <FormControl>
                      <Input placeholder="* (all services)" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name="expression"
              render={({ field }) => (
                <FormItem>
                  <FormLabel className="detail-label">Expression</FormLabel>
                  <FormControl>
                    <Input
                      className="font-mono"
                      placeholder="error_rate > 0.05"
                      {...field}
                    />
                  </FormControl>
                  <FormDescription className="font-mono text-[10px] text-muted-foreground/60">
                    error_rate · p50 · p95 · throughput · log_count · z_score ·
                    health_score · error_rate_delta · p95_delta ·
                    throughput_delta
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <FormField
                control={form.control}
                name="forSeconds"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel className="detail-label">For (sec)</FormLabel>
                    <FormControl>
                      <Input type="number" min={0} {...field} value={String(field.value ?? "")} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="cooldownS"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel className="detail-label">
                      Cooldown (sec)
                    </FormLabel>
                    <FormControl>
                      <Input type="number" min={0} {...field} value={String(field.value ?? "")} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="repeatS"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel className="detail-label">Repeat (sec)</FormLabel>
                    <FormControl>
                      <Input type="number" min={0} {...field} value={String(field.value ?? "")} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="notifyResolve"
                render={({ field }) => (
                  <FormItem className="flex items-end pb-1">
                    <FormControl>
                      <label className="flex items-center gap-2">
                        <input
                          type="checkbox"
                          checked={field.value}
                          onChange={field.onChange}
                          onBlur={field.onBlur}
                          name={field.name}
                          ref={field.ref}
                          className="accent-primary"
                        />
                        <span className="text-xs text-muted-foreground">
                          Notify resolve
                        </span>
                      </label>
                    </FormControl>
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name="webhookUrl"
              render={({ field }) => (
                <FormItem>
                  <FormLabel className="detail-label">Webhook URL</FormLabel>
                  <FormControl>
                    <Input
                      placeholder="https://hooks.slack.com/services/..."
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            <div className="flex gap-2">
              <Button
                type="submit"
                size="sm"
                disabled={form.formState.isSubmitting}
              >
                {form.formState.isSubmitting ? "Saving..." : "Save rule"}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setShowManual(false)}
              >
                Cancel
              </Button>
            </div>
          </form>
        </Form>
      )}
    </div>
  );
}
