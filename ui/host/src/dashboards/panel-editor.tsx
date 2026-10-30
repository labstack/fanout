import { Alert, Loader, Modal } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { DashboardSpec, DashboardTime, Panel, VarValue } from "../../../panels/types";
import { ApiError } from "../api-error";
import { formatFanql, type Formatted } from "../explore/api";
import { ExplorePage } from "../explore/page";
import { uniquePanelID } from "../explore/save-dialog";
import { defaultRange, type ExploreSearch } from "../explore/search";
import { dashboardsKey, patchDashboard, replaceDashboard } from "./api";

type PanelEditorProps = {
  dashboardId: string; version: number;
  /** The dashboard as it stands, including any unsaved layout. */
  spec: DashboardSpec;
  /** True when `spec` carries layout changes not yet saved; saving then writes the whole dashboard. */
  layoutChanged?: boolean;
  time: DashboardTime; vars: Record<string, VarValue>;
  /** The structured panel to edit; omitted adds a new panel. */
  panel?: Panel;
  onClose(): void;
};

function initialSearch(time: DashboardTime, panel: Panel | undefined, formatted: Formatted | undefined): ExploreSearch {
  const window = time.from && time.to ? { range: defaultRange, from: time.from, to: time.to } : { range: panel?.time?.range ?? time.range ?? defaultRange };
  if (!panel || !formatted) return { q: "", tab: "query", ...window };
  return { q: formatted.text, viz: panel.viz, tab: formatted.editable ? "query" : "builder", ...window };
}

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

/**
 * The edited panel with a new query: settings that still fit carry over, and
 * those tied to the old viz or the old result columns are dropped, so the
 * preview runs exactly what Save writes.
 */
function composePanel(panel: Panel, draft: Panel): Panel {
  const { query: oldQuery, sql: oldSQL, viz: oldViz, reduce, options, drill, click, ...kept } = panel;
  const next: Panel = { ...kept, id: draft.id, title: draft.title, viz: draft.viz, ...(draft.sql !== undefined ? { sql: draft.sql } : { query: draft.query }) };
  const sameViz = draft.viz === oldViz;
  const sameColumns = draft.sql !== undefined ? draft.sql === oldSQL : same(draft.query?.measures, oldQuery?.measures) && same(draft.query?.by, oldQuery?.by);
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
    if (key === "id" || key === "title" || key === "grid") continue;
    const before = (panel as Record<string, unknown>)[key], after = (next as Record<string, unknown>)[key];
    if (!same(before, after)) set[key] = after ?? null;
  }
  return set;
}

/** Adds or edits a structured panel with the Explore editor; the dashboard's variables apply. */
export default function PanelEditor({ dashboardId, version, spec, layoutChanged = false, time, vars, panel, onClose }: PanelEditorProps) {
  const client = useQueryClient();
  const formatted = useQuery({
    queryKey: ["panel-editor-format", dashboardId, panel?.id, version],
    queryFn: ({ signal }) => formatFanql({ query: panel!.query!, viz: panel!.viz }, signal),
    enabled: Boolean(panel?.query), staleTime: Infinity, retry: false,
  });
  const ready = !panel || formatted.isSuccess;
  const [search, setSearch] = useState<ExploreSearch>();
  const current = search ?? (ready ? initialSearch(time, panel, formatted.data) : undefined);

  const save = async (result: Panel) => {
    const added = panel ? undefined : { ...result, id: uniquePanelID("panel", spec.panels.map(p => p.id)) };
    const message = panel ? `Edited ${panel.title}` : `Added ${result.title}`;
    try {
      const record = layoutChanged
        // An unsaved layout is saved with the panel, as Duplicate and Remove do.
        ? await replaceDashboard(dashboardId, { ...spec, panels: added ? [...spec.panels, added] : spec.panels.map(p => p.id === panel!.id ? { ...result, id: p.id, title: p.title, grid: p.grid } : p) }, version, `${message} and saved layout`)
        : await patchDashboard(dashboardId, [added ? { op: "add_panel", panel: added } : { op: "update_panel", id: panel!.id, set: changes(panel!, { ...result, id: panel!.id, title: panel!.title }) }], version, message);
      await client.cancelQueries({ queryKey: ["dashboard", dashboardId], exact: true });
      client.setQueryData(["dashboard", dashboardId], record);
      void client.invalidateQueries({ queryKey: dashboardsKey });
      void client.invalidateQueries({ queryKey: ["dashboard-versions", dashboardId] });
      onClose();
    } catch (failure) {
      if (failure instanceof ApiError && failure.code === "dashboard_version_conflict") throw new Error("Someone saved this dashboard since you opened it. Close the editor, load the latest version, then redo your change.");
      if (failure instanceof ApiError && failure.problems.length > 0) throw new Error(failure.problems.map(p => p.message).join(" "));
      throw failure;
    }
  };

  // Escape first reaches the editor, which closes its completion list; only an unhandled Escape closes the dialog.
  return <Modal opened onClose={onClose} closeOnEscape={false} title={panel ? `Edit panel · ${panel.title}` : "Add panel"} size="min(1280px, 100vw - 32px)"
    onKeyDown={event => { if (event.key === "Escape" && !event.defaultPrevented && !event.nativeEvent.isComposing) onClose(); }}>
    {formatted.error ? <Alert color="bad" title="This panel's query could not be opened">{formatted.error.message}</Alert>
      : !current ? <Loader size="sm" aria-label="Loading the panel's query" />
      : <ExplorePage search={current} onSearch={setSearch}
          context={{ spec, vars, panel, compose: panel ? draft => composePanel(panel, draft) : undefined,
            save: { label: panel ? "Save panel" : "Add panel", run: save },
            initial: panel?.query && formatted.data && !formatted.data.editable ? { query: panel.query, formatted: formatted.data } : undefined }} />}
  </Modal>;
}
