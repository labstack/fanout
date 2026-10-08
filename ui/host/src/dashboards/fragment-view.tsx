import { Alert, Box, Modal, Stack, Text } from "@mantine/core";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { panelFragment, fragmentTitle, type PanelFragment } from "../../../panels/fragment";
import { resolvedVariables, interpolate } from "../../../panels/variables";
import type { Panel, Selection, VarValue } from "../../../panels/types";
import type { QueryBody } from "./api";
import type { DrillClient } from "./drill-client";
import { DrillDrawer } from "./drill";
import type { DrillTarget } from "./drill-state";
import { PanelCard } from "./panel-card";
import { VariableBar } from "./variable-bar";
import { TraceDetailView } from "./trace/detail";
import { useVariableOptions, type VariableResolver } from "./use-variables";
import { useBrushZoom } from "./use-brush-zoom";
import { drillSelection, panelHandlers } from "./panel-handlers";
import { fragmentPanelHeight } from "./layout";
const unavailableOptions: VariableResolver = async () => { throw new Error("Variable options unavailable"); };

export function FragmentView({ fragment, dark, onQuery, drillClient, resolveVariables, height }: {
  fragment: PanelFragment; dark: boolean; drillClient: DrillClient; height?: number;
  onQuery(body: Omit<QueryBody, "panels">): Promise<PanelFragment>;
  resolveVariables?: VariableResolver;
}) {
  const group = useId();
  const [shown, setShown] = useState(fragment);
  const [vars, setVars] = useState(fragment.vars ?? {});
  const [time, setTime] = useState(fragment.dashboard.time);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  const [staleAt, setStaleAt] = useState<number>();
  const updatedAt = useRef(Date.now());
  const generation = useRef(0);
  const [target, setTarget] = useState<DrillTarget>();
  const [view, setView] = useState<string>();
  const [windowHeight, setWindowHeight] = useState(() => window.innerHeight);
  useEffect(() => {
    const resize = () => setWindowHeight(window.innerHeight);
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, []);
  useEffect(() => {
    generation.current++;
    setShown(fragment); setVars(fragment.vars ?? {}); setTime(fragment.dashboard.time);
    setTarget(undefined); setView(undefined); setPending(false); setError(undefined); setStaleAt(undefined);
    resetBrush(); updatedAt.current = Date.now();
  }, [fragment]);
  useEffect(() => () => { generation.current++; }, []);
  const spec = shown.dashboard;
  const options = useVariableOptions(group, spec, time, vars, resolveVariables ?? unavailableOptions);
  const resolvedVars = useMemo(() => resolvedVariables(spec.variables, vars, options.currentData), [spec.variables, vars, options.currentData]);
  async function query(nextVars = vars, nextTime = time) {
    const active = ++generation.current;
    setVars(nextVars); setTime(nextTime); setTarget(undefined); setPending(true); setError(undefined); setStaleAt(updatedAt.current);
    const resolved = nextVars === vars ? resolvedVars : resolvedVariables(spec.variables, nextVars, options.currentData);
    try {
      const next = panelFragment(await onQuery({ dashboard: spec, time: nextTime, vars: resolved }));
      if (generation.current !== active) return;
      setShown(next); updatedAt.current = Date.now(); setStaleAt(undefined);
    } catch (cause) {
      if (generation.current === active) setError(cause instanceof Error ? cause.message : "This view could not be refreshed.");
    } finally { if (generation.current === active) setPending(false); }
  }
  const setVariable = (name: string, value: VarValue | undefined) => {
    const next = { ...vars }; if (value === undefined) delete next[name]; else next[name] = value;
    void query(next);
  };
  const { zoom, reset: resetBrush, resetZoom, zoomed } = useBrushZoom(time, next => void query(vars, next));
  const point = (panel: Panel, selection: Selection) => {
    const selected = drillSelection(panel, shown.results.find(r => r.id === panel.id), selection, vars);
    if (!selected) return;
    if (selected.vars !== vars) void query(selected.vars);
    setTarget(selected.target);
  };
  const card = (panel: Panel, cardHeight: number) => <PanelCard panel={panel} title={interpolate(panel.title, resolvedVars)} result={shown.results.find(r => r.id === panel.id)}
    group={group} height={cardHeight} loading={pending} staleAt={staleAt} editing={false} agentAvailable={false} vars={resolvedVars}
    range={panel.time?.range ?? time.range} compare={time.compare === "previous_period"} onVariable={setVariable}
    {...panelHandlers(panel, shown.results.find(r => r.id === panel.id), setVariable, selection => point(panel, selection))}
    onZoom={zoom} zoomed={zoomed} onZoomReset={resetZoom} onView={() => setView(panel.id)} traceLinks="button" />;
  const viewed = spec.panels.find(p => p.id === view);
  return <Stack gap="md" p="md">
    {spec.panels.length > 1 && <Text data-fragment-header fw={600}>{fragmentTitle(shown)}</Text>}
    <VariableBar variables={spec.variables ?? []} vars={vars} options={options.data ?? {}} onChange={setVariable} />
    {options.error && <Alert color="bad">Variable options could not be loaded.</Alert>}
    {error && <Alert color="bad">{error}</Alert>}
    {spec.panels.map(panel => <Box key={panel.id} h={height ?? fragmentPanelHeight(panel)}>{panel.id !== view && card(panel, height ?? fragmentPanelHeight(panel))}</Box>)}
    {shown.trace && <TraceDetailView result={shown.trace} dark={dark} />}
    <Modal opened={Boolean(viewed)} onClose={() => setView(undefined)} fullScreen aria-label={viewed?.title} closeButtonProps={{ "aria-label": "Close panel view" }}>
      {viewed && <Box h="calc(100vh - 120px)">{card(viewed, Math.max(40, windowHeight - 140))}</Box>}
    </Modal>
    <DrillDrawer client={drillClient} spec={spec} time={time} vars={resolvedVars} target={target} onChange={setTarget} />
  </Stack>;
}
