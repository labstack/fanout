import { Alert, Loader, Modal } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { DashboardSpec, DashboardTime, Panel, VarValue } from "../../../panels/types";
import { ApiError } from "../api-error";
import { formatFanql, type Formatted } from "../explore/api";
import { ExplorePage } from "../explore/page";
import { uniquePanelID } from "../explore/save-dialog";
import { defaultRange, type ExploreSearch } from "../explore/search";
import { dashboardsKey, patchDashboard } from "./api";

type PanelEditorProps = {
  dashboardId: string; version: number; spec: DashboardSpec; time: DashboardTime; vars: Record<string, VarValue>;
  /** The structured panel to edit; omitted adds a new panel. */
  panel?: Panel;
  onClose(): void;
};

function initialSearch(time: DashboardTime, panel: Panel | undefined, formatted: Formatted | undefined): ExploreSearch {
  const window = time.from && time.to ? { range: defaultRange, from: time.from, to: time.to } : { range: panel?.time?.range ?? time.range ?? defaultRange };
  if (!panel || !formatted) return { q: "", tab: "query", ...window };
  return { q: formatted.text, viz: panel.viz, tab: formatted.editable ? "query" : "builder", ...window };
}

/** Adds or edits a structured panel with the Explore editor; the dashboard's variables apply. */
export default function PanelEditor({ dashboardId, version, spec, time, vars, panel, onClose }: PanelEditorProps) {
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
    const operation = panel
      ? { op: "update_panel" as const, id: panel.id, set: { query: result.query, viz: result.viz } }
      : { op: "add_panel" as const, panel: { ...result, id: uniquePanelID("panel", spec.panels.map(p => p.id)) } };
    try {
      const record = await patchDashboard(dashboardId, [operation], version, panel ? `Edited ${panel.title}` : `Added ${result.title}`);
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

  return <Modal opened onClose={onClose} title={panel ? `Edit panel · ${panel.title}` : "Add panel"} size="min(1280px, 100vw - 32px)">
    {formatted.error ? <Alert color="bad" title="This panel's query could not be opened">{formatted.error.message}</Alert>
      : !current ? <Loader size="sm" aria-label="Loading the panel's query" />
      : <ExplorePage search={current} onSearch={setSearch}
          context={{ spec, vars, save: { label: panel ? "Save panel" : "Add panel", run: save }, initial: panel?.query && formatted.data && !formatted.data.editable ? { query: panel.query, formatted: formatted.data } : undefined }} />}
  </Modal>;
}
