import { Alert, Button, Group, Modal, SegmentedControl, Select, Stack, TextInput } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { DashboardTime, Panel } from "../../../panels/types";
import { ApiError } from "../api-error";
import { createDashboard, dashboardsKey, getDashboard, listDashboards, patchDashboard } from "../dashboards/api";

const titleLimit = 80;

/** Panel titles are limited to 80 code points. */
export function defaultTitle(text: string): string {
  const points = Array.from(text);
  return points.length <= titleLimit ? text : `${points.slice(0, titleLimit - 1).join("")}…`;
}

/** The first id of `base`, `base_2`, `base_3`… not already on the dashboard. */
function uniquePanelID(base: string, taken: readonly string[]): string {
  if (!taken.includes(base)) return base;
  for (let n = 2; ; n++) if (!taken.includes(`${base}_${n}`)) return `${base}_${n}`;
}

export function SaveDialog({ panel, time, onClose, onSaved }: {
  panel: Panel; time: DashboardTime; onClose(): void; onSaved(dashboardId: string): void;
}) {
  const client = useQueryClient();
  const dashboards = useQuery({ queryKey: dashboardsKey, queryFn: listDashboards });
  const [target, setTarget] = useState<"existing" | "new">("existing");
  const [dashboardId, setDashboardId] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [title, setTitle] = useState(defaultTitle(panel.title));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const mode = dashboards.data?.length === 0 ? "new" : target;
  const ready = title.trim() !== "" && (mode === "new" ? name.trim() !== "" : dashboardId !== null);

  const save = async () => {
    if (!ready || saving) return;
    setSaving(true); setError(undefined);
    try {
      let id: string;
      if (mode === "new") {
        // A dashboard holds at least one panel, so it is created with this one.
        const record = await createDashboard({ version: 1, name: name.trim(), time: time.from && time.to ? { from: time.from, to: time.to } : { range: time.range ?? "1h" }, panels: [{ ...panel, id: "explore", title: title.trim() }] });
        id = record.id;
      } else {
        const record = await getDashboard(dashboardId!);
        const added = { ...panel, id: uniquePanelID("explore", record.spec.panels.map(p => p.id)), title: title.trim() };
        await patchDashboard(record.id, [{ op: "add_panel", panel: added }], record.version, `Added ${added.title} from Explore`);
        id = record.id;
      }
      void client.invalidateQueries({ queryKey: dashboardsKey });
      void client.invalidateQueries({ queryKey: ["dashboard", id] });
      onSaved(id);
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "dashboard_version_conflict") setError("Someone saved that dashboard a moment ago. Save again to add the panel to the latest version.");
      else if (failure instanceof ApiError && failure.problems.length > 0) setError(failure.problems.map(p => p.message).join(" "));
      else setError(failure instanceof Error ? failure.message : "Save failed");
    } finally { setSaving(false); }
  };

  return <Modal opened onClose={() => { if (!saving) onClose(); }} title="Save to dashboard">
    <Stack gap="md">
      {dashboards.data && dashboards.data.length > 0 && <SegmentedControl fullWidth value={mode} onChange={value => setTarget(value === "new" ? "new" : "existing")} data={[{ value: "existing", label: "Existing dashboard" }, { value: "new", label: "New dashboard" }]} />}
      {mode === "existing"
        ? <Select label="Dashboard" placeholder="Choose a dashboard" searchable value={dashboardId} onChange={setDashboardId} data={(dashboards.data ?? []).map(d => ({ value: d.id, label: d.name }))} />
        : <TextInput label="Dashboard name" value={name} onChange={event => setName(event.currentTarget.value)} data-autofocus />}
      <TextInput label="Panel title" value={title} onChange={event => setTitle(event.currentTarget.value)} />
      {dashboards.error && <Alert color="bad">Dashboards could not be loaded.</Alert>}
      {error && <Alert color="bad" title="Not saved">{error}</Alert>}
      <Group justify="flex-end" gap="xs">
        <Button variant="default" disabled={saving} onClick={onClose}>Cancel</Button>
        <Button loading={saving} disabled={!ready} onClick={() => void save()}>Save</Button>
      </Group>
    </Stack>
  </Modal>;
}
