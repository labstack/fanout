import { Button, Group, Modal, Select, Stack, TextInput } from "@mantine/core";
import { useEffect, useState } from "react";
import type { WidgetType } from "../dashboard-layout";
import { configString, type WidgetConfig } from "./data";
import type { Widget } from "./widget-card";

type Field = "service" | "severity" | "search" | "trace_id";

const fields: Partial<Record<WidgetType, Field[]>> = {
  overview: ["service"],
  topology: ["service"],
  performance: ["service"],
  logs: ["service", "severity", "search"],
  trace: ["trace_id"],
};

export function configurable(type: WidgetType): boolean {
  return (fields[type]?.length ?? 0) > 0;
}

export function ConfigureWidget({ opened, widget, services, onClose, onSave }: { opened: boolean; widget: Widget; services: string[]; onClose: () => void; onSave: (config: WidgetConfig) => void }) {
  const [draft, setDraft] = useState<Record<Field, string>>({ service: "", severity: "", search: "", trace_id: "" });
  useEffect(() => {
    if (!opened) return;
    setDraft({ service: configString(widget.config, "service"), severity: configString(widget.config, "severity"), search: configString(widget.config, "search"), trace_id: configString(widget.config, "trace_id") });
  }, [opened, widget.config]);
  const wanted = fields[widget.type] ?? [];
  const serviceOptions = [...new Set([...services, draft.service].filter(Boolean))];

  function save() {
    const next: WidgetConfig = { ...(widget.config ?? {}) };
    for (const key of wanted) {
      const value = draft[key].trim();
      if (value) next[key] = value; else delete next[key];
    }
    onSave(next);
    onClose();
  }

  return <Modal opened={opened} onClose={onClose} title={`Configure ${widget.title}`} centered>
    <form onSubmit={(event) => { event.preventDefault(); save(); }}>
      <Stack>
        {wanted.includes("service") && <Select label="Service" placeholder="All services" data={serviceOptions} value={draft.service || null} onChange={(value) => setDraft({ ...draft, service: value ?? "" })} clearable searchable aria-label="Service" />}
        {wanted.includes("severity") && <Select label="Severity" placeholder="All severities" data={["ERROR", "WARN", "INFO", "DEBUG"]} value={draft.severity || null} onChange={(value) => setDraft({ ...draft, severity: value ?? "" })} clearable aria-label="Severity" />}
        {wanted.includes("search") && <TextInput label="Search" placeholder="Text the log body must contain" value={draft.search} onChange={(event) => setDraft({ ...draft, search: event.currentTarget.value })} aria-label="Search" />}
        {wanted.includes("trace_id") && <TextInput label="Trace id" placeholder="Leave empty for the most relevant recent trace" value={draft.trace_id} onChange={(event) => setDraft({ ...draft, trace_id: event.currentTarget.value })} ff="monospace" aria-label="Trace id" />}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button type="submit">Save</Button>
        </Group>
      </Stack>
    </form>
  </Modal>;
}
