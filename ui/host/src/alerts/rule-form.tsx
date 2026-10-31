import { Alert, Autocomplete, Button, Group, Menu, Modal, NumberInput, Stack, Switch, Text, TextInput, Textarea } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { listServices, type Rule, type RuleDraft } from "./api";
import { percentMistake } from "./values";

/** Starters for the documented variables (site docs: reference/alert-expressions). */
const templates = [
  { label: "Error rate", expression: "error_rate > 0.05 && throughput > 10" },
  { label: "p95 latency", expression: "p95 > 500 && throughput > 10" },
  { label: "Traffic drop", expression: "throughput_delta < -50" },
  { label: "Log volume", expression: "log_count > 1000" },
] as const;

const blank: RuleDraft = { name: "", description: "", enabled: true, service: "", expression: "", for_seconds: 60, cooldown_s: 300, repeat_interval_s: 3600, webhook_url: "", webhook_headers: "", webhook_template: "", notify_on_resolve: false };

function draftOf(rule?: Rule): RuleDraft {
  if (!rule) return blank;
  const { id: _id, created_at: _created, updated_at: _updated, ...draft } = rule;
  return { ...blank, ...draft };
}

/** Why webhook headers are invalid: they must be a JSON object of strings. */
function headersProblem(headers: string | undefined): string | undefined {
  if (!headers?.trim()) return undefined;
  let value: unknown;
  try { value = JSON.parse(headers); } catch { return "Enter a JSON object"; }
  if (typeof value !== "object" || value === null || Array.isArray(value)) return "Enter a JSON object";
  const name = Object.keys(value).find(key => typeof (value as Record<string, unknown>)[key] !== "string");
  return name === undefined ? undefined : `Header ${name} must be a string`;
}

/** Creates or replaces a rule; the server checks the expression and returns why it is invalid. */
export function RuleForm({ rule, onClose, onSave }: { rule?: Rule; onClose(): void; onSave(draft: RuleDraft): Promise<void> }) {
  const [draft, setDraft] = useState<RuleDraft>(() => draftOf(rule));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const set = (patch: Partial<RuleDraft>) => { setDraft(current => ({ ...current, ...patch })); setError(undefined); };
  const headers = headersProblem(draft.webhook_headers);
  const ready = draft.name.trim() !== "" && draft.expression.trim() !== "" && !headers;
  // A misspelled service saves, but would never fire; say so without blocking.
  const services = useQuery({ queryKey: ["alerting", "services"], queryFn: ({ signal }) => listServices(signal), staleTime: 60_000, retry: false });
  const service = draft.service?.trim() ?? "";
  const unseen = service !== "" && services.data !== undefined && !services.data.includes(service);
  const mistake = percentMistake(draft.expression);
  const seconds = (label: string, key: "for_seconds" | "cooldown_s" | "repeat_interval_s", description: string) =>
    <NumberInput label={label} description={description} min={0} step={30} value={draft[key]} onChange={value => set({ [key]: typeof value === "number" ? value : 0 })} />;

  const save = async () => {
    setSaving(true);
    try { await onSave({ ...draft, name: draft.name.trim(), expression: draft.expression.trim() }); }
    catch (failure) { setError(failure instanceof Error ? failure.message : "The rule could not be saved"); }
    finally { setSaving(false); }
  };

  return <Modal opened onClose={() => { if (!saving) onClose(); }} title={rule ? `Edit rule · ${rule.name}` : "New alert rule"} size="lg">
    <Stack gap="sm">
      <TextInput label="Name" required value={draft.name} onChange={event => set({ name: event.currentTarget.value })} data-autofocus />
      <TextInput label="Description" value={draft.description ?? ""} onChange={event => set({ description: event.currentTarget.value })} />
      <Autocomplete label="Service" description="Leave empty to evaluate every service." data={services.data ?? []} value={draft.service ?? ""} onChange={value => set({ service: value })} />
      {unseen && <Text size="xs" c="warn" mt={-8} role="status">No telemetry from {service} in the last hour. Check the name; the rule still saves.</Text>}
      <Stack gap={4}>
        <Group justify="space-between" align="flex-end">
          <Text size="sm" fw={500}>Expression</Text>
          <Menu position="bottom-end">
            <Menu.Target><Button size="compact-xs" variant="subtle">Start from a template</Button></Menu.Target>
            <Menu.Dropdown>{templates.map(t => <Menu.Item key={t.label} onClick={() => set({ expression: t.expression })}>{t.label}<Text size="xs" c="dimmed" ff="monospace">{t.expression}</Text></Menu.Item>)}</Menu.Dropdown>
          </Menu>
        </Group>
        <Textarea aria-label="Expression" required autosize minRows={2} ff="monospace" value={draft.expression} onChange={event => set({ expression: event.currentTarget.value })}
          description="A CEL condition over one service's rollup, for example error_rate > 0.05 && throughput > 10. error_rate is 0 to 1; p95 is in milliseconds." />
        {mistake && <Text size="xs" c="warn" role="status">{mistake}</Text>}
      </Stack>
      <Group grow align="flex-start">
        {seconds("Hold for (s)", "for_seconds", "Condition must hold this long")}
        {seconds("Cooldown (s)", "cooldown_s", "Quiet period after resolving")}
        {seconds("Repeat every (s)", "repeat_interval_s", "0 delivers once")}
      </Group>
      <TextInput label="Webhook URL" value={draft.webhook_url ?? ""} onChange={event => set({ webhook_url: event.currentTarget.value })} />
      <Textarea label="Webhook headers" description="A JSON object, for example {&quot;Authorization&quot;: &quot;Bearer …&quot;}" autosize minRows={1} ff="monospace" value={draft.webhook_headers ?? ""} error={headers} onChange={event => set({ webhook_headers: event.currentTarget.value })} />
      <Textarea label="Webhook body template" description="Go text/template; leave empty for the default payload." autosize minRows={1} ff="monospace" value={draft.webhook_template ?? ""} onChange={event => set({ webhook_template: event.currentTarget.value })} />
      <Group>
        <Switch label="Notify when resolved" checked={draft.notify_on_resolve} onChange={event => set({ notify_on_resolve: event.currentTarget.checked })} />
        <Switch label="Enabled" checked={draft.enabled} onChange={event => set({ enabled: event.currentTarget.checked })} />
      </Group>
      {error && <Alert color="bad" title="Not saved">{error}</Alert>}
      <Group justify="flex-end" gap="xs">
        <Button variant="default" disabled={saving} onClick={onClose}>Cancel</Button>
        <Button loading={saving} disabled={!ready} onClick={() => void save()}>{rule ? "Save rule" : "Create rule"}</Button>
      </Group>
    </Stack>
  </Modal>;
}
