import { Alert, Box, Button, Group, Loader, Modal, Stack, Text } from "@mantine/core";
import { useEffect, useId, useRef, useState } from "react";
import { panelFragment, type PanelFragment } from "../../../panels/fragment";
import { currentValue, interpolate } from "../../../panels/variables";
import { brushRange } from "../../../panels/interaction";
import type { DashboardTime, Panel, Selection, VarValue } from "../../../panels/types";
import type { QueryBody } from "./api";
import type { DrillClient } from "./drill-client";
import { DrillDrawer } from "./drill";
import { makeDrill, type DrillTarget } from "./drill-state";
import { PanelCard } from "./panel-card";
import { VariableBar } from "./variable-bar";
import { TraceDetailView } from "./trace/detail";

type Options = Record<string, { value: string; count?: number }[]>;
export function FragmentView({ fragment, dark, onQuery, drillClient, resolveVariables, height = 360 }: {
  fragment: PanelFragment; dark: boolean; drillClient: DrillClient; height?: number;
  onQuery(body: Omit<QueryBody, "panels">): Promise<PanelFragment>;
  resolveVariables?: (body: Omit<QueryBody, "panels" | "widths" | "compare">, signal?: AbortSignal) => Promise<Options>;
}) {
  const group = useId();
  const [shown, setShown] = useState(fragment);
  const [vars, setVars] = useState(fragment.vars ?? {});
  const [time, setTime] = useState(fragment.dashboard.time);
  const [options, setOptions] = useState<Options>({});
  const [optionsPending, setOptionsPending] = useState(false);
  const [optionsError, setOptionsError] = useState<string>();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const [staleAt, setStaleAt] = useState<number>();
  const updatedAt = useRef(Date.now());
  const generation = useRef(0);
  const [target, setTarget] = useState<DrillTarget>();
  const [view, setView] = useState<string>();
  const zoomRestore = useRef<DashboardTime | undefined>(undefined);
  const lastZoom = useRef<string | undefined>(undefined);
  useEffect(() => {
    generation.current++;
    setShown(fragment); setVars(fragment.vars ?? {}); setTime(fragment.dashboard.time);
    setTarget(undefined); setView(undefined); setPending(false); setError(undefined); setStaleAt(undefined);
    zoomRestore.current = undefined; lastZoom.current = undefined; updatedAt.current = Date.now();
  }, [fragment]);
  useEffect(() => () => { generation.current++; }, []);
  const spec = shown.dashboard;
  const hasQueryVariables = spec.variables?.some(v => v.kind === "query");
  useEffect(() => {
    if (!hasQueryVariables) { setOptions({}); setOptionsPending(false); setOptionsError(undefined); return; }
    const controller = new AbortController();
    setOptions({}); setOptionsPending(true); setOptionsError(undefined);
    if (!resolveVariables) { setOptionsPending(false); setOptionsError("Variable options unavailable"); return; }
    resolveVariables({ dashboard: spec, time, vars }, controller.signal).then(next => {
      if (!controller.signal.aborted) setOptions(next);
    }).catch(() => {
      if (!controller.signal.aborted) setOptionsError("Variable options could not be loaded.");
    }).finally(() => { if (!controller.signal.aborted) setOptionsPending(false); });
    return () => controller.abort();
  }, [spec, time, vars, hasQueryVariables, resolveVariables]);
  const displayVars = Object.fromEntries((spec.variables ?? []).map(v => [v.name, currentValue(v, vars, options[v.name])])) as Record<string, VarValue>;
  async function query(nextVars = vars, nextTime = time) {
    const active = ++generation.current;
    setVars(nextVars); setTime(nextTime); setTarget(undefined); setPending(true); setError(undefined); setStaleAt(updatedAt.current);
    // Use the same variable precedence as dashboards without erasing supplied
    // All, scalar or list values at the bridge boundary.
    const resolved = { ...nextVars };
    for (const variable of spec.variables ?? []) {
      const value = currentValue(variable, nextVars, options[variable.name]);
      if (variable.kind !== "query" || value !== "") resolved[variable.name] = value;
    }
    try {
      const next = panelFragment(await onQuery({ dashboard: spec, time: nextTime, vars: resolved }));
      if (generation.current !== active) return;
      setShown(next); updatedAt.current = Date.now(); setStaleAt(undefined);
    } catch (cause) {
      if (generation.current === active) setError(cause instanceof Error ? cause.message : "This view could not be refreshed.");
    } finally { if (generation.current === active) setPending(false); }
  }
  const current = useRef(query); current.current = query;
  useEffect(() => {
    const match = /^(\d+)(s|m)$/.exec(time.refresh ?? "off");
    if (!match || Number(match[1]) === 0) return;
    const timer = window.setInterval(() => void current.current(), Number(match[1]) * (match[2] === "s" ? 1000 : 60000));
    return () => window.clearInterval(timer);
  }, [time.refresh]);
  const setVariable = (name: string, value: VarValue | undefined) => {
    const next = { ...vars }; if (value === undefined) delete next[name]; else next[name] = value;
    void query(next);
  };
  const zoom = (from: number, to: number) => {
    const absolute = brushRange(from, to); if (!absolute) return;
    const key = `${absolute.from}/${absolute.to}`;
    if (lastZoom.current === key) return;
    zoomRestore.current ??= time; lastZoom.current = key;
    void query(vars, { ...absolute, refresh: "off", compare: time.compare });
  };
  const resetZoom = () => { const previous = zoomRestore.current; if (!previous) return; zoomRestore.current = undefined; lastZoom.current = undefined; void query(vars, previous); };
  const point = (panel: Panel, selection: Selection) => {
    const result = shown.results.find(r => r.id === panel.id); if (!result) return;
    const nextTarget = makeDrill(panel, result, selection);
    if (!nextTarget) return;
    const first = Object.values(selection.dimensions)[0]; const variable = panel.click?.set_variable;
    if (variable && first !== undefined) setVariable(variable, first);
    if (nextTarget) setTarget(nextTarget);
  };
  const card = (panel: Panel, cardHeight: number) => <PanelCard panel={panel} title={interpolate(panel.title, displayVars)} result={shown.results.find(r => r.id === panel.id)}
    group={group} height={cardHeight} loading={pending} staleAt={staleAt} editing={false} agentAvailable={false} vars={displayVars}
    range={panel.time?.range ?? time.range} compare={time.compare === "previous_period"} onVariable={setVariable}
    onSelect={panel.click && (panel.viz === "service_map" || !panel.drill) ? value => setVariable(panel.click!.set_variable, value) : undefined}
    onPoint={panel.viz === "service_map" || panel.click || panel.drill || shown.results.find(r => r.id === panel.id)?.frame?.columns.some(c => c.name === "trace_id") || panel.options?.columns?.some(c => c.format === "trace_link") ? selection => point(panel, selection) : undefined}
    onZoom={zoom} zoomed={Boolean(zoomRestore.current)} onZoomReset={resetZoom} onView={() => setView(panel.id)} />;
  const viewed = spec.panels.find(p => p.id === view);
  return <Stack gap="md" p="md">
    <Group justify="space-between"><Text fw={600}>{spec.name}</Text><Button size="compact-sm" variant="default" aria-label="Refresh panels" onClick={() => void query()}>Refresh</Button></Group>
    {optionsPending ? <Loader size="sm" aria-label="Loading variables" /> : <VariableBar variables={spec.variables ?? []} vars={vars} options={options} onChange={setVariable} />}
    {optionsError && <Alert color="bad">{optionsError}</Alert>}
    {error && <Alert color="bad">{error}</Alert>}
    {spec.panels.map(panel => <Box key={panel.id} h={height}>{card(panel, height)}</Box>)}
    {shown.trace && <TraceDetailView result={shown.trace} dark={dark} />}
    <Modal opened={Boolean(viewed)} onClose={() => setView(undefined)} fullScreen aria-label={viewed?.title} closeButtonProps={{ "aria-label": "Close panel view" }}>
      {viewed && <Box h="calc(100vh - 120px)">{card(viewed, window.innerHeight - 140)}</Box>}
    </Modal>
    <DrillDrawer client={drillClient} spec={spec} time={time} vars={vars} target={target} onChange={setTarget} />
  </Stack>;
}
