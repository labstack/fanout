import { ActionIcon, Badge, Button, Group, NumberInput, Select, Stack, Text, TextInput } from "@mantine/core";
import { Plus, X } from "@phosphor-icons/react";
import { useState } from "react";
import type { Query } from "../../../panels/types";
import type { TelemetrySchema } from "../sql-editor/schema";
import { fieldPattern, operators, parseWhere, printWhere, type Condition } from "./where";


const sources = ["spans", "logs", "metrics"] as const;
// Measures without a field argument; every other builder measure takes one field.
const fieldless = new Set(["count", "rate", "error_rate", "share"]);
// Measures with extra arguments stay in the query text, not the builder.
const textOnly = new Set(["quantile"]);
const measurePattern = new RegExp(String.raw`^([a-z_][a-z0-9_]*)\((|${fieldPattern})\)$`);

type Measure = { fn: string; field: string };
/** A known measure of at most one field; predicates such as count_if stay chips. */
function parseMeasure(entry: string, known: readonly string[]): Measure | undefined {
  const match = measurePattern.exec(entry.trim());
  if (!match || textOnly.has(match[1]) || !known.includes(match[1])) return undefined;
  if (fieldless.has(match[1]) !== (match[2] === "")) return undefined;
  return { fn: match[1], field: match[2] };
}
const printMeasure = (m: Measure) => `${m.fn}(${fieldless.has(m.fn) ? "" : m.field})`;

function fieldsOf(schema: TelemetrySchema, source: string) {
  const signal = schema.signals[source];
  const columns = (signal?.columns ?? []).filter(c => c.type !== "map" && c.type !== "time");
  const attributes = (signal?.attributes ?? []).map(a => ({ name: `${a.scope === "resource" ? "resource" : "attributes"}['${a.key.replaceAll("'", "''")}']`, type: a.type }));
  return [...columns, ...attributes];
}

/** Keeps entries the builder cannot show as rows, so nothing is silently dropped. */
function Chip({ text, onRemove }: { text: string; onRemove(): void }) {
  return <Badge variant="light" color="gray" size="lg" tt="none" rightSection={<ActionIcon size="xs" variant="subtle" color="gray" aria-label={`Remove ${text}`} onClick={onRemove}><X size={10} /></ActionIcon>}><code>{text}</code></Badge>;
}

/** `variables` are the dashboard's variable names; filters may compare with `$name`. */
export function Builder({ query, schema, variables = [], onChange }: { query: Query; schema: TelemetrySchema; variables?: readonly string[]; onChange(next: Query): void }) {
  const fields = fieldsOf(schema, query.from);
  const names = new Set(fields.map(f => f.name));
  const numeric = new Set(fields.filter(f => f.type === "number" || f.type === "int" || f.type === "float").map(f => f.name));
  const fieldData = fields.map(f => f.name);
  const known = schema.measures ?? [];
  const where = query.where ?? [];
  const measures = query.measures ?? [];
  const by = query.by ?? [];
  const buckets = schema.buckets ?? [];
  // A filter row without a value stays here, at its place among the rows, so the query never filters on an empty value.
  const [draft, setDraft] = useState<{ at: number; condition: Condition }>();
  // What the last source switch removed.
  const [removed, setRemoved] = useState<string>();
  const set = (patch: Partial<Query>) => { setRemoved(undefined); onChange({ ...query, ...patch }); };

  const switchSource = (from: Query["from"]) => {
    const next = new Set(fieldsOf(schema, from).map(f => f.name));
    const keep = (name: string) => next.has(name) || name.startsWith("attributes[") || name.startsWith("resource[");
    const kept = {
      where: where.filter(entry => { const c = parseWhere(entry); return !c || keep(c.field); }),
      measures: measures.filter(entry => { const m = parseMeasure(entry, known); return !m || !m.field || keep(m.field); }),
      by: by.filter(keep),
    };
    const dropped = [...measures.filter(m => !kept.measures.includes(m)), ...where.filter(w => !kept.where.includes(w)), ...by.filter(b => !kept.by.includes(b))];
    // Every source counts rows, so an aggregate keeps a measure.
    if (kept.measures.length === 0 && measures.length > 0) kept.measures = ["count()"];
    setDraft(undefined);
    setRemoved(dropped.length > 0 ? `Removed what ${from} does not have: ${dropped.join(", ")}.` : undefined);
    onChange({ ...query, from, ...kept });
  };
  const replace = <T,>(list: T[], index: number, value: T) => list.map((item, i) => i === index ? value : item);
  const remove = <T,>(list: T[], index: number) => list.filter((_, i) => i !== index);
  const rowField = (name: string) => names.has(name) ? fieldData : [name, ...fieldData];

  return <Stack gap="sm" data-builder>
    <Select label="Source" w={200} allowDeselect={false} value={query.from} data={[...sources]} onChange={value => value && value !== query.from && switchSource(value as Query["from"])} />
    {removed && <Text size="xs" c="dimmed" role="status" data-builder-removed>{removed}</Text>}

    <Stack gap={6}>
      <Text size="sm" fw={500}>Measures</Text>
      {measures.map((entry, index) => {
        const m = parseMeasure(entry, known);
        if (!m) return <Group key={index}><Chip text={entry} onRemove={() => set({ measures: remove(measures, index) })} /></Group>;
        return <Group key={index} gap="xs" wrap="nowrap">
          <Select aria-label={`Measure ${index + 1} function`} w={160} allowDeselect={false} searchable value={m.fn} data={(schema.measures ?? []).filter(fn => !textOnly.has(fn))}
            onChange={fn => fn && set({ measures: replace(measures, index, printMeasure({ fn, field: fieldless.has(fn) ? "" : m.field || (numeric.values().next().value ?? "") })) })} />
          {!fieldless.has(m.fn) && <Select aria-label={`Measure ${index + 1} field`} w={260} searchable value={m.field || null} data={measureFields(m.fn, fieldData, numeric, rowField(m.field))}
            onChange={field => field && set({ measures: replace(measures, index, printMeasure({ ...m, field })) })} />}
          <ActionIcon variant="subtle" color="gray" aria-label={`Remove measure ${index + 1}`} onClick={() => set({ measures: remove(measures, index) })}><X size={14} /></ActionIcon>
        </Group>;
      })}
      <Group><Button size="compact-sm" variant="subtle" leftSection={<Plus size={12} />} onClick={() => set({ measures: [...measures, "count()"] })}>Add measure</Button></Group>
    </Stack>

    <Stack gap={6}>
      <Text size="sm" fw={500}>Filters</Text>
      {(draft ? [...where.slice(0, draft.at), draft.condition, ...where.slice(draft.at)] : where).map((entry, index) => {
        const isDraft = draft?.at === index;
        // The row's place in the query; rows after the draft sit one earlier there.
        const at = draft && index > draft.at ? index - 1 : index;
        const drop = () => { if (draft && at < draft.at) setDraft({ ...draft, at: draft.at - 1 }); set({ where: remove(where, at) }); };
        const c = typeof entry === "string" ? parseWhere(entry) : entry;
        if (!c) return <Group key={index}><Chip text={entry as string} onRemove={drop} /></Group>;
        const update = (patch: Partial<Condition>) => {
          const next = { ...c, ...patch };
          // A schema field's type decides quoting; an attribute keeps how it was typed.
          if (names.has(next.field)) next.quoted = !numeric.has(next.field);
          if (!next.value.trim()) {
            // A cleared value takes the row out of the query until it has one again.
            setDraft({ at, condition: next });
            if (!isDraft) set({ where: remove(where, at) });
            return;
          }
          if (isDraft) setDraft(undefined);
          const printed = printWhere(next, variables);
          set({ where: isDraft ? [...where.slice(0, at), printed, ...where.slice(at)] : replace(where, at, printed) });
        };
        return <Group key={index} gap="xs" wrap="nowrap">
          <Select aria-label={`Filter ${index + 1} field`} w={260} searchable allowDeselect={false} value={c.field} data={rowField(c.field)} onChange={field => field && update({ field })} />
          <Select aria-label={`Filter ${index + 1} operator`} w={90} allowDeselect={false} value={c.op} data={[...operators]} onChange={op => op && update({ op: op as Condition["op"] })} />
          {c.field === "service" && schema.services?.length
            ? <Select aria-label={`Filter ${index + 1} value`} w={220} searchable value={c.value || null} data={[...variables.map(name => `$${name}`), ...schema.services.map(s => s.value)]} onChange={value => update({ value: value ?? "" })} />
            : <TextInput aria-label={`Filter ${index + 1} value`} w={220} placeholder={isDraft ? "Value" : undefined} value={c.value} onChange={event => update({ value: event.currentTarget.value })} />}
          <ActionIcon variant="subtle" color="gray" aria-label={`Remove filter ${index + 1}`} onClick={() => isDraft ? setDraft(undefined) : drop()}><X size={14} /></ActionIcon>
        </Group>;
      })}
      <Group><Button size="compact-sm" variant="subtle" leftSection={<Plus size={12} />} disabled={draft !== undefined} onClick={() => setDraft({ at: where.length, condition: { field: "service", op: "=", value: "", quoted: true } })}>Add filter</Button></Group>
    </Stack>

    <Group gap="md" align="flex-end">
      <Stack gap={6}>
        <Text size="sm" fw={500}>Group by</Text>
        <Group gap="xs">
          {by.map((name, index) => <Chip key={name} text={name} onRemove={() => set({ by: remove(by, index) })} />)}
          <Select aria-label="Add group by field" placeholder="Add field" w={220} searchable value={null} data={fieldData.filter(name => !by.includes(name))} onChange={name => name && set({ by: [...by, name] })} />
        </Group>
      </Stack>
      <Select label="Every" w={120} clearable placeholder="No time buckets" value={query.bucket ?? null} data={query.bucket && !buckets.includes(query.bucket) ? [...buckets, query.bucket] : buckets} onChange={bucket => set({ bucket: bucket ?? undefined })} />
      <NumberInput label="Limit" w={120} min={1} max={1000} value={query.limit ?? ""} onChange={value => set({ limit: typeof value === "number" ? value : undefined })} />
    </Group>
  </Stack>;
}

// Statistics need numbers; only count_distinct accepts any field.
function measureFields(fn: string, all: string[], numeric: Set<string>, withCurrent: string[]): string[] {
  const options = fn === "count_distinct" ? all : all.filter(name => numeric.has(name));
  return [...new Set([...withCurrent.filter(name => !all.includes(name)), ...options])];
}
