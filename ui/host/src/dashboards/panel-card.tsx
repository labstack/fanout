import { ActionIcon, Box, Button, Center, Group, Loader, Menu, Paper, Stack, Text, Tooltip, useComputedColorScheme } from "@mantine/core";
import { ArrowsOut, ArrowCounterClockwise, ChatCircleText, Copy, DotsThree, Info, ListMagnifyingGlass, Trash, WarningCircle } from "@phosphor-icons/react";
import type { Panel, PanelResult, Selection, VarValue } from "../../../panels/types";
import type { AnnotationsResponse } from "../../../panels/annotations";
import { panelTimeLabel } from "../../../panels/interaction";
import { logConstants } from "../../../panels/rows";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type Ref } from "react";
import { fonts } from "../../../tokens";
import { PanelData, PanelSpec } from "./inspect";
import type { MapView } from "./viz/service-map";
import { ChartHintContext, compactHint } from "./chart-keyboard";
import { Viz } from "./viz";

export function PanelCard({ panel, title, result, loading, compare, range, height, group, editing, agentAvailable, annotations, vars, onSelect, onPoint, onVariable, onZoom, onRangePending, onZoomReset, zoomed, onView, onCopyLink, onExplain, onFix, onRetry, onRemove, onDuplicate, staleAt, traceLinks, suspended = false, menuRef }: {
  compare?: boolean; range?: string; panel: Panel; title: string; result?: PanelResult; loading: boolean; height: number; group: string; editing: boolean; agentAvailable: boolean;
  annotations?: AnnotationsResponse; vars?: Record<string, VarValue>; traceLinks?: "button";
  onSelect?: (value: string) => void; onView?(): void; onCopyLink?: () => void; onExplain?: () => void; onFix?: () => void; onRetry?(): void; onRemove?: () => void; onDuplicate?: () => void; staleAt?: number;
  suspended?: boolean; menuRef?: Ref<HTMLButtonElement>;
  onVariable?: (name: string, value: string) => void; onPoint?: (selection: Selection) => void; onZoom?: (from: number, to: number) => void;
  zoomed?: boolean; onZoomReset?: () => void; onRangePending?(pending: boolean): void;
}) {
  const [keyboardHint, setKeyboardHint] = useState<string>();
  const chartHint = useMemo(() => ({setHint: setKeyboardHint}), []);
  const dark = useComputedColorScheme("light") === "dark";
  const card = useRef<HTMLDivElement>(null);
  const [width,setWidth] = useState(0);
  useLayoutEffect(()=>{
    const el=card.current;if(!el)return;
    setWidth(el.getBoundingClientRect().width);
    const observer=new ResizeObserver(([entry])=>{if(entry)setWidth(entry.contentRect.width);});observer.observe(el);
    return ()=>observer.disconnect();
  },[]);
  const small = width > 0 && width < 360;
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!staleAt) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, [staleAt]);
  const [view, setView] = useState("Chart");
  const [mapView, setMapView] = useState<MapView>();
  const onMapView = useCallback((next: MapView) => setMapView(next), []);
  const hint = panel.click ? `click to filter by ${panel.click.set_variable}`
    : panel.viz === "traces" || (panel.drill === "traces" || result?.frame?.columns.some(c => c.name === "trace_id")) && panel.viz === "table" ? "click to open trace"
    : panel.drill === "logs" ? "click for matching logs"
    : panel.drill === "traces" && ["timeseries", "heatmap", "state_timeline"].includes(panel.viz) ? "click for exemplar traces" : undefined;
  const bodyHeight = Math.max(40, height - 88);
  const constants = logConstants(panel,result?.frame);
  const source = constants.length ? [...new Set(constants.map(c => c.value))].join(" · ") : panel.sql ? "sql" : panel.query?.from ?? (panel.viz === "service_map" || panel.viz === "health" ? "spans" : panel.viz === "log_patterns" ? "logs" : "spec");
  const subtitle = `${panel.viz === "timeseries" ? "time series" : panel.viz.replaceAll("_", " ")} · ${source}${hint ? ` · ${hint}` : ""}`;
  const canvas = ["timeseries", "bar", "heatmap", "histogram", "scatter", "state_timeline", "gauge", "service_map"].includes(panel.viz);
  const scrolls = view !== "Chart" || !canvas || !result || result.status !== "ok" || result.frame?.truncated || result.previous?.truncated;
  const rows = ["table", "logs", "traces", "log_patterns", "text"].includes(panel.viz);
  const note = formatPanelNote(result?.frame?.note);
  const notes = [...new Set([
    staleAt ? `Stale: last updated ${relativeTime(staleAt, now)}` : undefined,
    result?.frame?.truncated || result?.previous?.truncated ? "Truncated: showing limited data" : undefined,
    note, result?.annotation_error,
    result?.annotation_scope?.limited ? "Annotation service scope is limited." : undefined,
  ].filter((text): text is string => Boolean(text)))];
  const body = useRef<HTMLDivElement>(null);
  const [fade, setFade] = useState<{bottom:number}>();
  useLayoutEffect(()=>{
    const el=body.current, parent=card.current;if(!el||!parent)return;
    if(!scrolls||!(rows||view==="Data")){setFade(undefined);return;}
    const measure=()=>{
      const bottom=Math.max(0,parent.getBoundingClientRect().bottom-el.getBoundingClientRect().bottom);
      const overflowing=el.scrollHeight>el.clientHeight;
      setFade(old=>overflowing?(old?.bottom===bottom?old:{bottom}):undefined);
    };
    measure();const observer=new ResizeObserver(measure);observer.observe(el);
    if(el.firstElementChild)observer.observe(el.firstElementChild);
    const mutations=new MutationObserver(measure);mutations.observe(el,{childList:true,subtree:true,attributes:true,characterData:true});
    return ()=>{observer.disconnect();mutations.disconnect();};
  },[result,view,rows,scrolls,height,small,notes.length]);
  return <Paper ref={card} withBorder radius="md" h="100%" p={0} style={{ display: "flex", flexDirection: "column", minWidth: 0, position:"relative" }} data-panel={panel.id} data-compact-views={small}>
    <Group justify="space-between" wrap="nowrap" gap="xs" px={16} pt={12} className={editing ? "panel-drag" : undefined} style={{ cursor: editing ? "grab" : undefined, flexShrink: 0 }}>
      <Group gap={6} wrap="nowrap" miw={0} style={{flex:1}}>
        <Box miw={0} style={{flex:1}}><Text data-panel-title fw={600} fz={15} truncate>{title}</Text><Text data-panel-subtitle title={keyboardHint ?? subtitle} fz={12} ff={fonts.display} c="dimmed" truncate={small ? undefined : true} style={{position: "relative", ...(small ? {overflowWrap:"anywhere"} : {})}}>
            {/* Preserve the subtitle's measured line box, including narrow cards. */}
            <span aria-hidden={keyboardHint ? true : undefined} style={{visibility: keyboardHint ? "hidden" : undefined}}>{subtitle}</span>
            {keyboardHint && <><span data-chart-hint aria-hidden="true" style={{position: "absolute", inset: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap"}}>{compactHint(keyboardHint)}</span></>}
          </Text>
          {panel.options?.highlight && <Text component="span" data-highlight-term title={`highlight: ${panel.options.highlight}`} fz={11} c="dimmed" style={{display:"inline-block",maxWidth:"100%",overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap",border:"1px solid var(--mantine-color-default-border)",borderRadius:4,padding:"0 5px"}}>highlight: {panel.options.highlight}</Text>}
        </Box>
        {panelTimeLabel(panel) && <Text size="xs" c="dimmed" role="status">{panelTimeLabel(panel)}</Text>}
        {panel.description && <Tooltip label={panel.description} multiline w={260}><ActionIcon variant="transparent" color="gray" size="xs" aria-label={`${title} description`}><Info size={14} /></ActionIcon></Tooltip>}
        {loading && result && <Loader size={12} aria-label="Refreshing" />}
      </Group>
      <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
      {!small && panel.viz !== "text" && <Group gap={0} wrap="nowrap" role="group" aria-label={`${title} view`} style={{ border: "1px solid var(--mantine-color-default-border)", borderRadius: 5, overflow: "hidden" }}>
        {["Chart", "Data", "Spec"].map(mode => <button key={mode} type="button" data-panel-view={mode} aria-pressed={view === mode} onClick={() => setView(mode)} style={{ border: 0, borderLeft: mode === "Chart" ? undefined : "1px solid var(--mantine-color-default-border)", padding: "2px 6px", fontSize: 11, fontFamily: "inherit", cursor: "pointer", background: view === mode ? "var(--mantine-color-default)" : "transparent", color: view === mode ? "var(--mantine-primary-color-filled)" : "var(--mantine-color-dimmed)", fontWeight: view === mode ? 600 : 400, boxShadow: view === mode ? "inset 0 -2px var(--mantine-primary-color-filled)" : undefined }}>{mode}</button>)}
      </Group>}
      {panel.viz === "service_map" && mapView?.canFit && <ActionIcon variant="subtle" color="gray" size="sm" aria-label={`Fit ${title} graph`} onClick={mapView.fit}><ArrowsOut size={16} /></ActionIcon>}
      {zoomed && onZoomReset && ["timeseries", "heatmap", "state_timeline"].includes(panel.viz) && <ActionIcon variant="subtle" color="gray" size="sm" aria-label={`Reset ${title} zoom`} onClick={onZoomReset}><ArrowCounterClockwise size={16} /></ActionIcon>}
      <Menu position="bottom-end" withinPortal>
        <Menu.Target><ActionIcon ref={menuRef} variant="subtle" color="gray" size="sm" aria-label={`${title} menu`}><DotsThree size={18} weight="bold" /></ActionIcon></Menu.Target>
        <Menu.Dropdown>
          {(small || panel.viz === "text") && <>
            <Menu.RadioGroup value={view} onChange={setView}>
              <Box role="group" aria-label={`${title} view`}>
                {(panel.viz === "text" ? ["Chart", "Spec"] : ["Chart", "Data", "Spec"]).map(mode => <Menu.RadioItem key={mode} value={mode} data-panel-view={mode}>{panel.viz === "text" && mode === "Chart" ? "Content" : mode}</Menu.RadioItem>)}
              </Box>
            </Menu.RadioGroup>
            <Menu.Divider />
          </>}
          {onView && <Menu.Item leftSection={<ArrowsOut size={14} />} onClick={onView}>View</Menu.Item>}
          {agentAvailable && onExplain && <Menu.Item leftSection={<ChatCircleText size={14} />} onClick={onExplain}>Explain in chat</Menu.Item>}
          {onCopyLink && <Menu.Item leftSection={<Copy size={14} />} onClick={onCopyLink}>Copy link</Menu.Item>}
          {onDuplicate && <Menu.Item leftSection={<Copy size={14} />} onClick={onDuplicate}>Duplicate</Menu.Item>}
          {onRemove && <><Menu.Divider /><Menu.Item color="bad" leftSection={<Trash size={14} />} onClick={onRemove}>Remove panel</Menu.Item></>}
        </Menu.Dropdown>
      </Menu>
      </Group>
    </Group>
    {result?.error && result.status !== "error" && <Group px={16} pt={8} gap="xs"><Stack gap={4} style={{flex: 1, minWidth: 0}}><Group gap={6}><WarningCircle size={18} weight="fill" color="var(--mantine-color-bad-filled)" /><Text size="sm" fw={500} c="bad">This panel failed</Text></Group><Text size="xs" c="dimmed" style={{overflowWrap: "anywhere"}}>{result.error}</Text></Stack>{onRetry && <Button size="compact-xs" variant="light" disabled={loading} onClick={onRetry}>Retry</Button>}</Group>}
    <Box ref={body} data-panel-body className="dashboard-panel-padding" style={{ flex: "1 1 0px", isolation: "isolate", minHeight: 0, minWidth: 0, padding: "16px", overflow: scrolls ? "auto" : "hidden", display: rows || view !== "Chart" ? "block" : "flex", flexDirection: "column" }}>
      {suspended ? <Center h="100%"><Text size="sm" c="dimmed">Shown in full-screen</Text></Center> : view === "Data" ? <PanelData panel={panel} result={result} /> : view === "Spec" ? <PanelSpec panel={panel} dark={dark} /> : !result && panel.viz !== "text" ? <Center style={{ minHeight: "100%", flexShrink: 0 }}><Loader size="sm" aria-label="Loading panel" /></Center>
        : result?.status === "error" ? <Center style={{ minHeight: "100%", flexShrink: 0 }}><Stack align="center" gap={4} maw={420}>
          <Group gap={6}><WarningCircle size={18} weight="fill" color="var(--mantine-color-bad-filled)" /><Text size="sm" fw={500} c="bad">This panel failed</Text></Group>
          <Text size="xs" c="dimmed" ta="center" style={{ overflowWrap: "anywhere" }}>{result.error}</Text>
          {onRetry && <Button size="compact-xs" variant="light" disabled={loading} onClick={onRetry}>Retry</Button>}
          {agentAvailable && onFix && <Button size="compact-xs" variant="light" onClick={onFix}>Ask Fanout to fix it</Button>}
        </Stack></Center>
        : result?.status === "empty" ? <Center style={{ minHeight: "100%", flexShrink: 0 }}><Stack align="center" gap={4} maw={420}>
          <ListMagnifyingGlass size={20} color="var(--mantine-color-dimmed)" />
          <Text size="sm" c="dimmed" ta="center">{result.diagnosis || "No data in this time range."}</Text>
        </Stack></Center>
        : <ChartHintContext.Provider value={chartHint}><Viz traceLinks={traceLinks} onMapView={onMapView} compare={compare} range={range} panel={panel} title={title} result={result} dark={dark} height={bodyHeight} group={group} annotations={annotations} vars={vars} onSelect={onSelect} onPoint={onPoint} onVariable={onVariable} onZoom={onZoom} onRangePending={onRangePending} /></ChartHintContext.Provider>}
    </Box>
    {notes.length > 0 && <Box data-panel-notes className="dashboard-panel-padding" pb={12} style={{ display: "flex", flexDirection: "column", gap: 4, flexShrink: 0, position: "relative", zIndex: 1, background: "inherit" }}>
      {notes.map(text => <Text key={text} data-panel-note fz={12} c="dimmed" role="status" title={text === note ? result?.frame?.note : undefined} style={{ overflowWrap: "anywhere" }}>{text}</Text>)}
    </Box>}
    {fade&&<Box data-panel-scroll-fade aria-hidden="true" style={{position:"absolute",left:16,right:16,bottom:fade.bottom,height:16,pointerEvents:"none",zIndex:1,background:"linear-gradient(to bottom, transparent, var(--mantine-color-body))"}}/>}
  </Paper>;
}

function formatPanelNote(note?: string): string | undefined {
  const split = note?.match(/^Split at (\S+)( · .+)$/);
  if (!split) return note;
  const at = new Date(split[1]);
  if (!Number.isFinite(at.getTime())) return note;
  const stamp = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }).format(at);
  return `Split at ${stamp}${split[2]}`;
}

function relativeTime(at: number, now: number): string {
  const seconds = Math.max(0, Math.floor((now - at) / 1000));
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} minute${minutes === 1 ? "" : "s"} ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours} hour${hours === 1 ? "" : "s"} ago`;
}
