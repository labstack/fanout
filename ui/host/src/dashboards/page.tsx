import { Alert, Box, Button, Center, Group, Loader, Stack, Text, Title } from "@mantine/core";
import { WarningCircle } from "@phosphor-icons/react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { ALL, type DashboardSpec, type VarValue } from "../../../panels/types";
import { createDashboardPrompt, useFanoutApp, type TurnOptions } from "../app-context";
import { ApiError, dashboardsKey, getDashboard, listDashboards, queryExemplars, getTrace, resolveVariables } from "./api";
import { PanelGrid } from "./grid";
import { DrillDrawer } from "./drill";
import { parseDrill } from "./drill-state";
import { effectiveTime, type DashboardSearch } from "./search";
import { Toolbar } from "./toolbar";
import { HistoryDrawer } from "./history";
import { dashboardDataPredicate } from "./cache";
import { useBrushZoom } from "./use-brush-zoom";
import { usePanelResults } from "./use-panel-results";
import { useVariableOptions } from "./use-variables";
import { VariableBar } from "./variable-bar";
import { useShortcuts } from "./use-shortcuts";
import { ShortcutsHelp } from "./shortcuts-help";

import { resolvedVariables } from "../../../panels/variables";
import { drillSelection } from "./panel-handlers";
import { retryQuery } from "./query-policy";
import type { DrillClient } from "./drill-client";
const drillClient: DrillClient = { exemplars: queryExemplars, trace: getTrace };

const zoomOut: Record<string, string> = { "5m": "15m", "15m": "1h", "1h": "3h", "3h": "6h", "6h": "12h", "12h": "24h", "24h": "2d", "2d": "7d", "7d": "30d", "30d": "30d" };

export function DashboardPage({ dashboardId, search, onSearch, onOpen }: { dashboardId?: string; search: DashboardSearch; onSearch(next: DashboardSearch, replace?: boolean): void; onOpen(id: string, replace?: boolean): void }) {
  const { agentAvailable, openChat } = useFanoutApp();
  const list = useQuery({ queryKey: dashboardsKey, queryFn: listDashboards, staleTime: 15_000 });
  useEffect(() => {
    if (dashboardId || !list.data?.length) return;
    onOpen((list.data.find((d) => d.is_default) ?? list.data[0]).id, true);
  }, [dashboardId, list.data]);
  const record = useQuery({ queryKey: ["dashboard", dashboardId], queryFn: ({ signal }) => getDashboard(dashboardId!, signal), enabled: Boolean(dashboardId), retry: (count, error) => !(error instanceof ApiError && error.status === 404) && count < 2 });

  if (!dashboardId && list.error) return <Center mih="50vh"><Alert color="bad" title="Dashboards could not be loaded">{list.error.message}</Alert></Center>;
  if (!dashboardId && list.data?.length === 0) return <Center mih="50vh"><Stack align="center" gap="xs">
    <Title order={1} fz={24}>No dashboards yet</Title>
    <Text c="dimmed" size="sm" ta="center">Create a dashboard to explore your services.</Text>
    {agentAvailable && <Button mt="sm" size="sm" onClick={() => openChat(createDashboardPrompt)}>Create with AI</Button>}
  </Stack></Center>;
  if (!dashboardId || record.isLoading) return <Center mih="50vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">Loading your dashboard…</Text></Center>;
  if (record.error instanceof ApiError && record.error.status === 404) {
    return <Center mih="50vh"><Stack align="center" gap="xs">
      <Title order={1} fz={24}>This dashboard isn&apos;t here</Title>
      <Text c="dimmed" size="sm" ta="center">The link may be out of date, or the dashboard may have been deleted.</Text>
      {list.data?.length ? <Button mt="sm" variant="default" size="sm" onClick={() => onOpen((list.data.find((d) => d.is_default) ?? list.data[0]).id, true)}>Open your default dashboard</Button> : null}
    </Stack></Center>;
  }
  if (record.error || !record.data) return <Center mih="50vh"><Text c="dimmed" size="sm">Your dashboard is unavailable. Try refreshing.</Text></Center>;
  return <Loaded key={record.data.id} id={record.data.id} version={record.data.version} spec={record.data.spec} search={search} onSearch={onSearch} agentAvailable={agentAvailable} openChat={openChat} />;
}

function Loaded({ id, version, spec, search, onSearch, agentAvailable, openChat }: { id: string; version: number; spec: DashboardSpec; search: DashboardSearch; onSearch(next: DashboardSearch, replace?: boolean): void; agentAvailable: boolean; openChat(prompt?: string, options?: TurnOptions): void }) {
  // Loading this owner-scoped record proves manage-own capability on the server.
  const canManage = true;
  const [historyOpen, setHistoryOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const region = useRef<HTMLElement>(null);
  const client = useQueryClient();
  const [restoreRefresh, setRestoreRefresh] = useState<number>();
  const { zoom, reset: resetBrush, resetZoom, zoomed } = useBrushZoom(search, onSearch);
  const time = effectiveTime(spec, search);
  const [refresh, setRefresh] = useState(time.refresh ?? "30s");
  const compare = search.compare ? search.compare === "1" : spec.time.compare === "previous_period";
  const vars = search.vars ?? {};
  const options = useVariableOptions(`dashboard-${id}`, spec, time, vars, resolveVariables, retryQuery);
  const resolvedVars = useMemo(() => resolvedVariables(spec.variables, vars, options.currentData), [spec.variables, vars, options.currentData]);
  // Panel widths come from the layout and the viewport, known at first
  // render, so the first batch already carries them and no second request
  // follows once the grid has measured itself.
  const [viewport, setViewport] = useState(() => window.innerWidth);
  useEffect(() => {
    let timer = 0;
    const onResize = () => { window.clearTimeout(timer); timer = window.setTimeout(() => setViewport(window.innerWidth), 300); };
    window.addEventListener("resize", onResize);
    return () => { window.removeEventListener("resize", onResize); window.clearTimeout(timer); };
  }, []);
  const widths = useMemo(() => {
    const container = Math.max(320, Math.min(viewport - 64, 1600));
    return Object.fromEntries(spec.panels.map((p) => [p.id, (container / 12) * (p.grid?.w ?? p.width ?? 6)]));
  }, [viewport, spec]);
  const [visible, setVisible] = useState<string[]>(() => spec.panels.map((p) => p.id));
  const currentVisible = useMemo(() => visible.filter((panelId) => spec.panels.some((panel) => panel.id === panelId)), [visible, spec.panels]);
  const data = usePanelResults({ dashboardId: id, version, spec, time, vars: resolvedVars, compare, widths, visible: currentVisible, refresh, enabled: options.ready });
  useEffect(() => {
    if (restoreRefresh !== version) return;
    // Run after both hooks have committed their observers to the restored keys.
    // Do not restart a new-spec request already in flight.
    void client.refetchQueries({ predicate: dashboardDataPredicate(id), type: "active", stale: true }, { cancelRefetch: false });
  }, [restoreRefresh, version, id, client]);
  const annotations = useMemo(() => data.annotations ? {
    ...data.annotations,
    deploys: spec.annotations?.deploys === false ? [] : data.annotations.deploys,
    anomalies: spec.annotations?.anomalies === false ? [] : data.annotations.anomalies,
  } : undefined, [data.annotations, spec.annotations?.deploys, spec.annotations?.anomalies]);
  const loadError = options.error ?? data.error;
  const setVar = (name: string, value: VarValue | undefined) => {
    const next = { ...vars };
    if (value === undefined) delete next[name];
    else next[name] = value;
    onSearch({ ...search, vars: next }, true);
  };

  const edit = () => onSearch({ ...search, edit: search.edit === "1" ? undefined : "1" });
  useShortcuts(region, {
    r: () => data.refetch(), e: edit, h: () => setHistoryOpen(true), "?": () => setHelpOpen(true),
    f: target => { const panel = target.closest<HTMLElement>("[data-panel]")?.dataset.panel; if (panel && spec.panels.some(p => p.id === panel)) onSearch({...search, view: panel}); },
  }, historyOpen || helpOpen || Boolean(search.view || search.drill));
  return <Box component="main" ref={region} aria-label="Dashboard" maw={1600} mx="auto" px={{ base: "md", sm: "xl" }} pt="lg" pb="xl">
    <Stack gap="sm" mb="md">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
        <Box miw={0}>
          <Title order={1} fz={28} lts="-0.02em">{spec.name}</Title>
          {spec.description && <Text c="dimmed" size="sm" mt={2}>{spec.description}</Text>}
        </Box>
        <Toolbar time={time} refresh={refresh} compare={compare} editing={search.edit === "1"} fetching={data.fetching} updatedAt={data.updatedAt}
          onRange={(range) => { resetBrush(); onSearch({ ...search, range, from: undefined, to: undefined }); }}
          onAbsolute={(from, to) => { resetBrush(); onSearch({ ...search, range: undefined, from, to }); }}
          onZoomOut={() => {
            if (time.from && time.to) {
              const from = Date.parse(time.from);
              const to = Date.parse(time.to);
              const half = (to - from) / 2;
              onSearch({ ...search, range: undefined, from: new Date(from - half).toISOString(), to: new Date(to + half).toISOString() });
            } else onSearch({ ...search, range: zoomOut[time.range ?? "1h"] ?? "1h", from: undefined, to: undefined });
          }}
          onRefresh={setRefresh} onRefreshNow={data.refetch}
          onCompare={(on) => onSearch({ ...search, compare: on ? "1" : "0" }, true)}
          onEdit={edit} onHistory={() => setHistoryOpen(true)} onShortcuts={() => setHelpOpen(true)} />
      </Group>
      <VariableBar variables={spec.variables ?? []} vars={vars} options={options.data ?? {}} onChange={setVar} />
      {Object.entries(vars).filter(([, v]) => v !== ALL).length > 0 && <Group gap={6}>
        {Object.entries(vars).filter(([, v]) => v !== ALL).map(([name, value]) => <Button key={name} size="compact-xs" variant="light" aria-label={`Remove filter ${name}`} onClick={() => setVar(name, undefined)}>${name} = {Array.isArray(value) ? value.join(", ") : value} ×</Button>)}
      </Group>}
      {data.annotationError && <Alert color="warn" title="Annotations unavailable">{data.annotationError}</Alert>}
      {data.annotations?.truncated && <Text size="xs" c="warn" role="status">Annotation history is limited.</Text>}
    </Stack>
    {loadError && <Alert color="bad" icon={<WarningCircle size={18} weight="fill" />} mb="md" title={options.error ? "Variables could not be loaded" : "Panels could not be loaded"}>
      {loadError.message}
      {loadError instanceof ApiError && <ul>{loadError.problems.map((problem, index) =>
        <li key={index}>{problem.path}: {problem.message}{problem.hint ? ` (${problem.hint})` : ""}</li>)}</ul>}
    </Alert>}
    <PanelGrid dashboardId={id} version={version} spec={spec} vars={resolvedVars} results={data.results} annotations={annotations} fetching={data.fetching} fetchingIds={data.fetchingIds} staleAt={data.staleAt} time={{ ...time, compare: compare ? "previous_period" : undefined }} onEditExit={() => onSearch({ ...search, edit: undefined })} editing={search.edit === "1"} view={search.view}
      canManage={canManage} agentAvailable={agentAvailable} onOpenChat={openChat} onVariable={setVar} onZoom={zoom} zoomed={zoomed} onZoomReset={resetZoom} onView={(view) => onSearch({ ...search, view }, view === undefined)} onVisible={setVisible}
      onPoint={(panel, selection) => {
        const selected = drillSelection(panel, data.results.get(panel.id), selection, vars);
        if (selected) onSearch({ ...search, vars: selected.vars, drill: JSON.stringify(selected.target) }, false);
      }} />
    <DrillDrawer client={drillClient} spec={spec} time={time} vars={resolvedVars} target={parseDrill(search.drill)} onChange={target => onSearch({ ...search, drill: target ? JSON.stringify(target) : undefined }, false)} />
    <HistoryDrawer id={id} currentVersion={version} opened={historyOpen} onClose={() => setHistoryOpen(false)} onRestored={record => {
      setRestoreRefresh(record.version);
      const ids = new Set(record.spec.panels.map(panel => panel.id));
      const drill = parseDrill(search.drill);
      const view = search.view && !ids.has(search.view) ? undefined : search.view;
      const nextDrill = drill && !ids.has(drill.panel_id) ? undefined : search.drill;
      if (view !== search.view || nextDrill !== search.drill) onSearch({ ...search, view, drill: nextDrill }, true);
    }} />
    <ShortcutsHelp opened={helpOpen} onClose={() => setHelpOpen(false)} />
  </Box>;
}
