import { Alert, Button, Loader, Modal, Text, TextInput } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { ALL, type DashboardSpec, type DashboardTime, type Panel, type VarValue } from "../../../panels/types";
import { ApiError } from "../api-error";
import { formatFanql, type Formatted } from "../explore/api";
import { ExplorePage } from "../explore/page";
import { uniquePanelID } from "../explore/save-dialog";
import { defaultRange, type ExploreSearch } from "../explore/search";
import { dashboardsKey, getDashboard, patchDashboard, replaceDashboard } from "./api";
import { useDiscard } from "./discard";

type PanelEditorProps = {
  dashboardId: string; version: number;
  /** The dashboard as it stands, including any unsaved layout. */
  spec: DashboardSpec;
  /** True when `spec` carries layout changes not yet saved; saving then writes the whole dashboard. */
  layoutChanged?: boolean;
  time: DashboardTime; vars: Record<string, VarValue>;
  /** The structured or SQL panel to edit; omitted adds a new panel. */
  panel?: Panel;
  onClose(): void;
};

function initialSearch(time: DashboardTime, panel: Panel | undefined, formatted: Formatted | undefined): ExploreSearch {
  const window = time.from && time.to ? { range: defaultRange, from: time.from, to: time.to } : { range: panel?.time?.range ?? time.range ?? defaultRange };
  if (panel?.sql !== undefined) return { q: "", sql: panel.sql, viz: panel.viz, tab: "sql", ...window };
  if (!panel || !formatted) return { q: "", tab: "query", ...window };
  return { q: formatted.text, viz: panel.viz, tab: formatted.editable ? "query" : "builder", ...window };
}

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

const fallback: Partial<Record<Panel["viz"], Panel["viz"]>> = { gauge: "stat", state_timeline: "timeseries" };

/**
 * The edited panel with a new query: settings that still fit carry over, and
 * those tied to the old viz, the old measures or the old result columns are
 * dropped, so the preview runs exactly what Save writes.
 */
function composePanel(panel: Panel, draft: Panel): Panel {
  const { query: oldQuery, sql: oldSQL, viz: oldViz, reduce, options, drill, click, unit, thresholds, better, min, max, ...kept } = panel;
  const next: Panel = { ...kept, id: draft.id, title: draft.title, viz: draft.viz, ...(draft.sql !== undefined ? { sql: draft.sql } : { query: draft.query }) };
  // A unit, thresholds and bounds describe what was measured; SQL keeps them, as its measures are unknown.
  const sameMeasures = draft.sql !== undefined || same(draft.query?.measures, oldQuery?.measures);
  const sameColumns = draft.sql !== undefined ? draft.sql === oldSQL : sameMeasures && same(draft.query?.by, oldQuery?.by);
  if (sameMeasures) Object.assign(next, { unit, thresholds, better, min, max });
  // A gauge needs bounds and a state timeline thresholds; without the old ones they fall back to the plain form of the same shape.
  else if (next.viz in fallback) next.viz = fallback[next.viz]!;
  const sameViz = next.viz === oldViz;
  if (sameViz && reduce) next.reduce = reduce;
  if (sameViz && sameColumns && options) next.options = options;
  if (drill && draft.query && draft.query.from === oldQuery?.from) next.drill = drill;
  if (click && draft.query && same(draft.query.by, oldQuery?.by)) next.click = click;
  return next;
}

/** update_panel fields: every changed field, with null removing one the new panel no longer has. */
function changes(panel: Panel, next: Panel): Record<string, unknown> {
  const set: Record<string, unknown> = {};
  for (const key of new Set([...Object.keys(panel), ...Object.keys(next)])) {
    if (key === "id" || key === "grid") continue;
    const before = (panel as Record<string, unknown>)[key], after = (next as Record<string, unknown>)[key];
    if (!same(before, after)) set[key] = after ?? null;
  }
  return set;
}

const shown = (value: VarValue | undefined) => value === undefined ? "not set" : value === ALL ? "All" : Array.isArray(value) ? value.join(", ") : value;

/** Adds or edits a panel with the Explore editor (Query, Builder or SQL); the dashboard's variables apply. */
export default function PanelEditor({ dashboardId, version: openedVersion, spec: openedSpec, layoutChanged = false, time, vars, panel: openedPanel, onClose }: PanelEditorProps) {
  const client = useQueryClient();
  // After a version conflict, the latest dashboard replaces the one the editor opened with; the draft stays.
  const [latest, setLatest] = useState<{ version: number; spec: DashboardSpec; panel?: Panel }>();
  const { version, spec, panel } = latest ?? { version: openedVersion, spec: openedSpec, panel: openedPanel };
  const [conflict, setConflict] = useState<"stale" | "loading" | string>();
  const loadLatest = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (conflict === "stale") loadLatest.current?.focus(); }, [conflict]);
  const formatted = useQuery({
    queryKey: ["panel-editor-format", dashboardId, openedPanel?.id, openedVersion],
    queryFn: ({ signal }) => formatFanql({ query: openedPanel!.query!, viz: openedPanel!.viz }, signal),
    enabled: Boolean(openedPanel?.query), staleTime: Infinity, retry: false,
  });
  const ready = !openedPanel?.query || formatted.isSuccess;
  const [search, setSearch] = useState<ExploreSearch>();
  const current = search ?? (ready ? initialSearch(time, openedPanel, formatted.data) : undefined);
  // New panels are named after their query unless a title is typed.
  const [title, setTitle] = useState(openedPanel?.title ?? "");
  const [queryDirty, setQueryDirty] = useState(false);
  const dirty = queryDirty || title.trim() !== (openedPanel?.title ?? "") || (openedPanel !== undefined && current?.viz !== undefined && current.viz !== openedPanel.viz);
  const discard = useDiscard(dirty, onClose, "this panel");
  const replaced = useRef(false);

  const reload = async () => {
    setConflict("loading");
    try {
      const record = await getDashboard(dashboardId);
      client.setQueryData(["dashboard", dashboardId], record);
      replaced.current = true;
      // A panel someone else removed is added back on save.
      setLatest({ version: record.version, spec: record.spec, panel: openedPanel && record.spec.panels.find(p => p.id === openedPanel.id) });
      setConflict(undefined);
    } catch (failure) {
      setConflict(failure instanceof Error ? failure.message : "The latest version could not be loaded.");
    }
  };

  const save = async (result: Panel) => {
    const name = title.trim() || result.title;
    const added = panel ? undefined : { ...result, title: name, id: uniquePanelID("panel", spec.panels.map(p => p.id)) };
    const message = panel ? `Edited ${name}` : `Added ${name}`;
    // Loading the latest version drops the unsaved layout with it.
    const withLayout = layoutChanged && !replaced.current;
    try {
      const record = withLayout
        // An unsaved layout is saved with the panel, as Duplicate and Remove do.
        ? await replaceDashboard(dashboardId, { ...spec, panels: added ? [...spec.panels, added] : spec.panels.map(p => p.id === panel!.id ? { ...result, id: p.id, title: name, grid: p.grid } : p) }, version, `${message} and saved layout`)
        : await patchDashboard(dashboardId, [added ? { op: "add_panel", panel: added } : { op: "update_panel", id: panel!.id, set: changes(panel!, { ...result, id: panel!.id, title: name }) }], version, message);
      await client.cancelQueries({ queryKey: ["dashboard", dashboardId], exact: true });
      client.setQueryData(["dashboard", dashboardId], record);
      void client.invalidateQueries({ queryKey: dashboardsKey });
      void client.invalidateQueries({ queryKey: ["dashboard-versions", dashboardId] });
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "dashboard_version_conflict") { setConflict("stale"); return; }
      if (failure instanceof ApiError && failure.problems.length > 0) throw new Error(failure.problems.map(p => p.message).join(" "));
      throw failure;
    }
  };

  const variables = spec.variables ?? [];
  return <Modal opened onClose={discard.request} closeOnEscape={false} title={openedPanel ? `Edit panel · ${openedPanel.title}` : "Add panel"} size="min(1280px, 100vw - 32px)" onKeyDown={discard.onKeyDown}>
    {discard.prompt}
    {conflict && <Alert color={conflict === "stale" || conflict === "loading" ? "warn" : "bad"} title="Someone saved this dashboard since you opened it" mb="sm">
      <Text size="sm">{conflict === "stale" || conflict === "loading" ? "Load the latest version, then save again. Your draft stays as it is." : conflict}</Text>
      <Button ref={loadLatest} mt="sm" size="compact-sm" loading={conflict === "loading"} onClick={() => void reload()}>Load latest</Button>
    </Alert>}
    <TextInput label="Panel title" px="lg" value={title} onChange={event => setTitle(event.currentTarget.value)} placeholder={openedPanel ? undefined : "Named after the query"} />
    {variables.length > 0 && <Text size="xs" c="dimmed" px="lg" pt="xs" data-editor-variables>
      Runs with the dashboard&apos;s variables: {variables.map((v, index) => <span key={v.name}>{index > 0 && " · "}<code>${v.name}</code> = {shown(vars[v.name])}</span>)}
    </Text>}
    {formatted.error ? <Alert color="bad" title="This panel's query could not be opened">{formatted.error.message}</Alert>
      : !current ? <Loader size="sm" role="status" aria-label="Loading the panel's query" />
      : <ExplorePage search={current} onSearch={setSearch}
          context={{ spec, vars, panel, compose: panel ? draft => composePanel(panel, draft) : undefined, onDirtyChange: setQueryDirty, cancel: discard.request,
            save: { label: openedPanel ? "Save panel" : "Add panel", run: save },
            initial: openedPanel?.query && formatted.data && !formatted.data.editable ? { query: openedPanel.query, formatted: formatted.data } : undefined }} />}
  </Modal>;
}
