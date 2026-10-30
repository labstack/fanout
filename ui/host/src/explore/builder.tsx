import { ActionIcon, Badge, Button, Group, NumberInput, Select, Stack, Text, TextInput } from "@mantine/core";
import { Plus, X } from "@phosphor-icons/react";
import type { Query } from "../../../panels/types";
import { operators, parseWhere, printWhere, type Condition, type Operator } from "./where";

/** The parts of GET /api/telemetry/schema the builder reads. */
export type BuilderSchema = {
  signals: Record<string, { columns: { name: string; type: string }[]; attributes?: { key: string; scope: string; type: string }[] }>;
  services?: { value: string }[];
  measures: string[];
};

const sources = ["spans", "logs", "metrics"] as const;
const buckets = ["auto", "1m", "5m", "15m", "1h"];
// Measures without a field argument; every other builder measure takes one field.
const fieldless = new Set(["count", "rate", "error_rate"]);
// Measures with extra arguments stay in the query text, not the builder.
const textOnly = new Set(["quantile", "share"]);

type Measure = { fn: string; field: string };
function parseMeasure(entry: string): Measure | undefined {
  const match = /^([a-z_][a-z0-9_]*)\(([^(),]*)\)$/.exec(entry.trim());
  return match && !textOnly.has(match[1]) ? { fn: match[1], field: match[2].trim() } : undefined;
}
const printMeasure = (m: Measure) => `${m.fn}(${fieldless.has(m.fn) ? "" : m.field})`;

function fieldsOf(schema: BuilderSchema, source: string) {
  const signal = schema.signals[source];
  const columns = (signal?.columns ?? []).filter(c => c.type !== "map" && c.type !== "time");
  const attributes = (signal?.attributes ?? []).map(a => ({ name: `${a.scope === "resource" ? "resource" : "attributes"}['${a.key.replaceAll("'", "''")}']`, type: a.type }));
  return [...columns, ...attributes];
}

/** Keeps entries the builder cannot show as rows, so nothing is silently dropped. */
function Chip({ text, onRemove }: { text: string; onRemove(): void }) {
  return <Badge variant="light" color="gray" size="lg" tt="none" rightSection={<ActionIcon size="xs" variant="subtle" color="gray" aria-label={`Remove ${text}`} onClick={onRemove}><X size={10} /></ActionIcon>}><code>{text}</code></Badge>;
}

export function Builder({ query, schema, onChange }: { query: Query; schema: BuilderSchema; onChange(next: Query): void }) {
  const fields = fieldsOf(schema, query.from);
  const names = new Set(fields.map(f => f.name));
  const numeric = new Set(fields.filter(f => f.type === "number" || f.type === "int" || f.type === "float").map(f => f.name));
  const fieldData = fields.map(f => f.name);
  const set = (patch: Partial<Query>) => onChange({ ...query, ...patch });
  const where = query.where ?? [];
  const measures = query.measures ?? [];
  const by = query.by ?? [];

  const switchSource = (from: Query["from"]) => {
    const next = new Set(fieldsOf(schema, from).map(f => f.name));
    const keep = (name: string) => next.has(name) || name.startsWith("attributes[") || name.startsWith("resource[");
    onChange({
      ...query, from,
      where: where.filter(entry => { const c = parseWhere(entry); return !c || keep(c.field); }),
      measures: measures.filter(entry => { const m = parseMeasure(entry); return !m || !m.field || keep(m.field); }),
      by: by.filter(keep),
    });
  };
  const replace = <T,>(list: T[], index: number, value: T) => list.map((item, i) => i === index ? value : item);
  const remove = <T,>(list: T[], index: number) => list.filter((_, i) => i !== index);
  const rowField = (name: string) => names.has(name) ? fieldData : [name, ...fieldData];

  return <Stack gap="sm" data-builder>
    <Select label="Source" w={200} allowDeselect={false} value={query.from} data={[...sources]} onChange={value => value && switchSource(value as Query["from"])} />

    <Stack gap={6}>
      <Text size="sm" fw={500}>Measures</Text>
      {measures.map((entry, index) => {
        const m = parseMeasure(entry);
        if (!m) return <Group key={index}><Chip text={entry} onRemove={() => set({ measures: remove(measures, index) })} /></Group>;
        return <Group key={index} gap="xs" wrap="nowrap">
          <Select aria-label={`Measure ${index + 1} function`} w={160} allowDeselect={false} searchable value={m.fn} data={schema.measures.filter(fn => !textOnly.has(fn))}
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
      {where.map((entry, index) => {
        const c = parseWhere(entry);
        if (!c) return <Group key={index}><Chip text={entry} onRemove={() => set({ where: remove(where, index) })} /></Group>;
        const update = (patch: Partial<Condition>) => { const next = { ...c, ...patch }; set({ where: replace(where, index, printWhere(next, numeric.has(next.field))) }); };
        return <Group key={index} gap="xs" wrap="nowrap">
          <Select aria-label={`Filter ${index + 1} field`} w={260} searchable allowDeselect={false} value={c.field} data={rowField(c.field)} onChange={field => field && update({ field })} />
          <Select aria-label={`Filter ${index + 1} operator`} w={90} allowDeselect={false} value={c.op} data={[...operators]} onChange={op => op && update({ op: op as Operator })} />
          {c.field === "service" && schema.services?.length
            ? <Select aria-label={`Filter ${index + 1} value`} w={220} searchable value={c.value || null} data={schema.services.map(s => s.value)} onChange={value => update({ value: value ?? "" })} />
            : <TextInput aria-label={`Filter ${index + 1} value`} w={220} value={c.value} onChange={event => update({ value: event.currentTarget.value })} />}
          <ActionIcon variant="subtle" color="gray" aria-label={`Remove filter ${index + 1}`} onClick={() => set({ where: remove(where, index) })}><X size={14} /></ActionIcon>
        </Group>;
      })}
      <Group><Button size="compact-sm" variant="subtle" leftSection={<Plus size={12} />} onClick={() => set({ where: [...where, printWhere({ field: "service", op: "=", value: schema.services?.[0]?.value ?? "" }, false)] })}>Add filter</Button></Group>
    </Stack>

    <Group gap="md" align="flex-end">
      <Stack gap={6}>
        <Text size="sm" fw={500}>Group by</Text>
        <Group gap="xs">
          {by.map((name, index) => <Chip key={name} text={name} onRemove={() => set({ by: remove(by, index) })} />)}
          <Select aria-label="Add group by field" placeholder="Add field" w={220} searchable value={null} data={fieldData.filter(name => !by.includes(name))} onChange={name => name && set({ by: [...by, name] })} />
        </Group>
      </Stack>
      <Select label="Every" w={120} clearable placeholder="No time buckets" value={query.bucket ?? null} data={buckets} onChange={bucket => set({ bucket: bucket ?? undefined })} />
      <NumberInput label="Limit" w={120} min={1} max={1000} value={query.limit ?? ""} onChange={value => set({ limit: typeof value === "number" ? value : undefined })} />
    </Group>
  </Stack>;
}

// Statistics need numbers; count_distinct and last accept any field.
function measureFields(fn: string, all: string[], numeric: Set<string>, withCurrent: string[]): string[] {
  const options = fn === "count_distinct" || fn === "last" ? all : all.filter(name => numeric.has(name));
  return [...new Set([...withCurrent.filter(name => !all.includes(name)), ...options])];
}
