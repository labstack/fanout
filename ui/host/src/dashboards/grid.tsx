import { Alert, Button, Group, Modal, Text } from "@mantine/core";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Responsive, type Layout } from "react-grid-layout/legacy";
import type { DashboardSpec, DashboardTime, Panel, PanelResult, Selection, VarValue } from "../../../panels/types";
import type { AnnotationsResponse } from "../../../panels/annotations";
import { ApiError, patchDashboard, replaceDashboard } from "./api";
import { interpolate } from "../../../panels/variables";
import { panelHandlers } from "./panel-handlers";
import { PanelCard } from "./panel-card";

/** Grid rows are 40 px with 12 px gaps; internal/dashboard/layout.go packs
 *  with the same row unit. */
import { rowHeight, margin, pixels, defaultRows } from "./layout";
export { rowHeight } from "./layout";

export type GridProps = {
  dashboardId: string; version: number; spec: DashboardSpec; vars: Record<string, VarValue>; results: Map<string, PanelResult>; fetching: boolean; editing: boolean; view?: string;
  fetchingIds?: string[]; staleAt?: Map<string, number>; time?: DashboardTime; onEditExit?(): void;
  annotations?: AnnotationsResponse;
  agentAvailable: boolean; onOpenChat(prompt?: string): void; onVariable(name: string, value: VarValue): void; onView(view?: string): void; onVisible(ids: string[]): void;
  onPoint?(panel: Panel, selection: Selection): void; onZoom?(from: number, to: number): void;
  zoomed?: boolean; onZoomReset?(): void;
};

let panelIdCounter = 0;
export function newPanelId(panels: Panel[]): string {
  let id: string;
  do {
    id = `p_${Date.now().toString(36)}_${(++panelIdCounter).toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
  } while (panels.some((panel) => panel.id === id));
  return id;
}

export function PanelGrid({ dashboardId, version, spec, vars, results, fetching, annotations, fetchingIds = [], staleAt = new Map(), time = spec.time, onEditExit, editing, view, agentAvailable, onOpenChat, onVariable, onPoint, onZoom, onZoomReset, zoomed, onView, onVisible }: GridProps) {
  const client = useQueryClient();
  const [layout, setLayout] = useState(() => spec.panels.map((p) => ({ i: p.id, x: p.grid?.x ?? 0, y: p.grid?.y ?? 0, w: p.grid?.w ?? 6, h: p.grid?.h ?? defaultRows(p) })));
  useEffect(() => { setLayout(spec.panels.map((p) => ({ i: p.id, x: p.grid?.x ?? 0, y: p.grid?.y ?? 0, w: p.grid?.w ?? 6, h: p.grid?.h ?? defaultRows(p) }))); }, [spec]);
  // Presentation minimum fits preview stat chrome + value/delta + area trend.
  // Keep saved coordinates intact; a layout is persisted only by an edit action.
  const visibleLayout = useMemo(() => layout.map(l => spec.panels.find(p => p.id === l.i)?.viz === "stat" ? { ...l, h: Math.max(4, l.h), minH: 4 } : l), [layout, spec.panels]);
  const [windowHeight, setWindowHeight] = useState(() => window.innerHeight);
  const [copyFeedback, setCopyFeedback] = useState<string>();
  useEffect(() => {
    const resize = () => setWindowHeight(window.innerHeight);
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, []);
  const container = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const gridReady = width > 0;
  const breakpoint = width >= 1100 ? "lg" : width >= 800 ? "md" : "sm";
  const canEdit = editing && breakpoint === "lg";
  // WidthProvider starts at 1280px and renders before its first observer
  // delivery (even with measureBeforeMount). Observe the stable container,
  // and never lay out panels at a guessed or hidden-container width.
  useLayoutEffect(() => {
    if (!container.current) return;
    setWidth(Math.max(0, Math.round(container.current.getBoundingClientRect().width)));
    const observer = new ResizeObserver(([entry]) => {
      if (entry) setWidth(Math.max(0, Math.round(entry.contentRect.width)));
    });
    observer.observe(container.current);
    return () => observer.disconnect();
  }, []);
  const visibleCallback = useRef(onVisible);
  visibleCallback.current = onVisible;

  useEffect(() => {
    if (!gridReady || !container.current || typeof IntersectionObserver === "undefined") return;
    const seen = new Set<string>(spec.panels.map((p) => p.id));
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        const id = (entry.target as HTMLElement).dataset.panel;
        if (!id) continue;
        if (entry.isIntersecting) seen.add(id); else seen.delete(id);
      }
      if (view && spec.panels.some((panel) => panel.id === view)) seen.add(view);
      visibleCallback.current([...seen]);
    }, { rootMargin: "200px" });
    container.current.querySelectorAll(".react-grid-item[data-panel]").forEach((el) => observer.observe(el));
    return () => observer.disconnect();
  }, [spec, view, gridReady]);

  const dirty = useMemo(() => spec.panels.some((p) => { const g = layout.find((l) => l.i === p.id); return g && (g.x !== p.grid?.x || g.y !== p.grid?.y || g.w !== p.grid?.w || g.h !== p.grid?.h); }), [layout, spec]);
  const withLayout = (panels: Panel[]) => ({ ...spec, panels: panels.map((p) => { const g = layout.find((l) => l.i === p.id); return g ? { ...p, grid: { x: g.x, y: g.y, w: g.w, h: g.h } } : p; }) });
  const save = useMutation({
    mutationFn: () => replaceDashboard(dashboardId, withLayout(spec.panels), version, "Edited layout"),
    onSuccess: (record) => { client.setQueryData(["dashboard", dashboardId], record); },
  });
  const remove = useMutation({
    mutationFn: (id: string) => dirty
      ? replaceDashboard(dashboardId, withLayout(spec.panels.filter((p) => p.id !== id)), version, "Removed a panel and saved layout")
      : patchDashboard(dashboardId, [{ op: "remove_panel", id }], version, "Removed a panel"),
    onSuccess: (record) => { client.setQueryData(["dashboard", dashboardId], record); },
  });
  const duplicate = useMutation({
    mutationFn: (panel: Panel) => {
      const copy = { ...panel, id: newPanelId(spec.panels), title: `${Array.from(panel.title).slice(0, 73).join("")} (copy)`, grid: undefined };
      if (dirty) {
        const panels = [...spec.panels];
        panels.splice(panels.findIndex((p) => p.id === panel.id) + 1, 0, copy);
        return replaceDashboard(dashboardId, withLayout(panels), version, "Duplicated a panel and saved layout");
      }
      return patchDashboard(dashboardId, [{ op: "add_panel", after: panel.id, panel: copy }], version, "Duplicated a panel");
    },
    onSuccess: (record) => { client.setQueryData(["dashboard", dashboardId], record); },
  });
  useEffect(() => { if (!editing) { save.reset(); remove.reset(); duplicate.reset(); } }, [editing]);
  const loadLatest = () => {
    save.reset();
    remove.reset();
    duplicate.reset();
    onEditExit?.();
    void client.invalidateQueries({ queryKey: ["dashboard", dashboardId] });
  };
  const copyLink = async (panelId: string) => {
    const url = new URL(window.location.href);
    url.searchParams.set("view", panelId);
    url.searchParams.delete("edit");
    try {
      await navigator.clipboard.writeText(url.toString());
      setCopyFeedback("Link copied");
    } catch {
      setCopyFeedback(`Copy this URL: ${url.toString()}`);
    }
  };
  const group = `dashboard-${dashboardId}`;

  const card = (panel: Panel, height: number) => <PanelCard panel={panel} title={interpolate(panel.title, vars)} result={results.get(panel.id)} loading={fetching && fetchingIds.includes(panel.id)} height={height} group={group} editing={canEdit} agentAvailable={agentAvailable}
    compare={time.compare === "previous_period"} range={panel.time?.range ?? time.range} annotations={annotations} vars={vars} onVariable={onVariable}
    {...panelHandlers(panel, results.get(panel.id), onVariable, onPoint ? selection => onPoint(panel, selection) : undefined)}
    onZoom={onZoom} zoomed={zoomed} onZoomReset={onZoomReset}
    onView={() => onView(panel.id)}
    onCopyLink={() => { void copyLink(panel.id); }}
    onExplain={() => onOpenChat(`Explain the panel "${interpolate(panel.title, vars)}" (panel id: ${panel.id}) on the dashboard "${spec.name}" (dashboard id: ${dashboardId}). Effective time range: ${JSON.stringify(panel.time ?? time)}. Resolved variables: ${JSON.stringify(vars)}. ${results.get(panel.id)?.status === "error" ? `It fails with: ${results.get(panel.id)?.error}. Please fix the panel.` : "What does it show right now, and is anything unusual?"}`)}
    staleAt={staleAt.get(panel.id)} onDuplicate={canEdit ? () => duplicate.mutate(panel) : undefined}
    onRemove={canEdit ? () => remove.mutate(panel.id) : undefined} />;

  const viewed = spec.panels.find((p) => p.id === view);
  const mutationError = editing ? save.error ?? remove.error ?? duplicate.error : null;
  const conflict = mutationError instanceof ApiError && mutationError.status === 409;
  // Responsive compaction can emit a layout on mount or viewport changes.
  // Only user drag/resize completions should become edits to persist.
  const changeLayout = (next: Layout) => {
    if (canEdit) setLayout(next.map(({ i, x, y, w, h }) => ({ i, x, y, w, h })));
  };
  return <div ref={container} style={{ minWidth: 0 }}>
    {editing && <Group justify="space-between" mb="sm" p="xs" style={{ border: "1px dashed var(--mantine-color-default-border)", borderRadius: 8 }}>
      <Text size="sm" c="dimmed">{canEdit ? "Drag a panel by its title and resize it from the corner. Changes are saved as a new version." : "The layout can be edited only on a wider screen."}</Text>
      <Button size="compact-sm" disabled={!dirty || !canEdit} loading={save.isPending} onClick={() => save.mutate()}>Save layout</Button>
    </Group>}
    {conflict && <Alert color="warn" mb="sm">Someone saved this dashboard since you opened it. Load the latest version, then redo your change. <Button size="compact-sm" onClick={loadLatest}>Load latest</Button></Alert>}
    {mutationError && !conflict && <Alert color="bad" mb="sm">{mutationError.message}</Alert>}
    {copyFeedback && <Alert role="status" mb="sm">{copyFeedback}</Alert>}
    {!gridReady && <div data-grid-placeholder style={{ height: pixels(Math.max(1, ...visibleLayout.map(l => l.y + l.h))) }} />}
    {gridReady && <Responsive width={width} className={`dashboard-grid${canEdit ? " dashboard-grid-editing" : ""}`} layouts={{ lg: visibleLayout, md: visibleLayout, sm: visibleLayout.map((l) => ({ ...l, x: 0, w: 12 })) }} breakpoints={{ lg: 1100, md: 800, sm: 0 }} cols={{ lg: 12, md: 12, sm: 12 }}
      rowHeight={rowHeight} margin={[margin, margin]} containerPadding={[0, 0]} compactType="vertical" isDraggable={canEdit} isResizable={canEdit} draggableHandle=".panel-drag" draggableCancel="button"
      onDragStop={changeLayout} onResizeStop={changeLayout}>
      {spec.panels.map((panel) => { const g = visibleLayout.find((l) => l.i === panel.id); return <div key={panel.id} data-panel={panel.id}>{card(panel, pixels(g?.h ?? 6))}</div>; })}
    </Responsive>}
    <Modal opened={Boolean(viewed)} onClose={() => onView(undefined)} fullScreen aria-label={viewed ? interpolate(viewed.title, vars) : undefined} closeButtonProps={{ "aria-label": "Close panel view" }}>
      {viewed && <div style={{ height: "calc(100vh - 120px)" }}>{card(viewed, windowHeight - 140)}</div>}
    </Modal>
  </div>;
}
