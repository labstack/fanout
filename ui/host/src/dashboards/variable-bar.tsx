import { Group, MultiSelect, Select, Text, TextInput } from "@mantine/core";
import { ALL, type Variable, type VarValue } from "../../../panels/types";
import { currentValue } from "../../../panels/variables";

export function VariableBar({ variables, vars, options, onChange }: {
  variables: Variable[]; vars: Record<string, VarValue>; options: Record<string, { value: string; count?: number }[]>; onChange(name: string, value: VarValue | undefined): void;
}) {
  if (variables.length === 0) return null;
  return <Group gap="sm" wrap="wrap" role="group" aria-label="Dashboard variables">
    {variables.map((variable) => {
      const value = currentValue(variable, vars, options[variable.name]);
      const choices = [
        ...(variable.include_all ? [{ value: ALL, label: "All" }] : []),
        ...(options[variable.name] ?? variable.options?.map((o) => ({ value: o })) ?? []).map((o) => ({ value: o.value, label: o.value })),
      ];
      const label = <Text size="xs" c="dimmed" ff="monospace">${variable.name}</Text>;
      if (variable.kind === "constant") return <TextInput key={variable.name} label={label} size="xs" value={variable.value} readOnly w={160} />;
      if (variable.kind === "text") return <TextInput key={`${variable.name}:${JSON.stringify(value)}`} label={label} size="xs" w={200} defaultValue={typeof value === "string" ? value : ""} onBlur={(e) => onChange(variable.name, e.currentTarget.value)} onKeyDown={(e) => { if (e.key === "Enter") e.currentTarget.blur(); }} />;
      if (variable.multi) return <MultiSelect key={variable.name} label={label} size="xs" w={260} searchable clearable data={choices.filter((c) => c.value !== ALL)} value={Array.isArray(value) ? value : value && value !== ALL ? [value] : []} placeholder={variable.include_all ? "All" : "None"} onChange={(next) => onChange(variable.name, next.length ? next : variable.include_all ? ALL : [])} />;
      return <Select key={variable.name} label={label} size="xs" w={220} searchable allowDeselect={false} data={choices} value={Array.isArray(value) ? value[0] ?? null : value || null} onChange={(next) => next !== null && onChange(variable.name, next)} />;
    })}
  </Group>;
}
