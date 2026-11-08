import { LoadingSkeleton } from "../loading-skeleton";
import { DataState, Notice } from "@labstack/ui";
import { logConstants } from "../../../panels/rows";
import { useMergedRef } from "@mantine/hooks";
import { ActionIcon, Box, Button, Group, Loader, Menu, Paper, Text, Tooltip, useComputedColorScheme } from "@mantine/core";
import { ArrowsOut, ArrowCounterClockwise, ChatCircleText, Copy, DotsThree, Info, Trash, WarningCircle } from "@phosphor-icons/react";
import type { Panel, Selection, VarValue } from "../../../panels/types";
import type { PanelDisplayResult } from "./panel-result";
import type { AnnotationsResponse } from "../../../panels/annotations";
import { panelTimeLabel } from "../../../panels/interaction";
import { useCallback, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type Ref } from "react";
import { fonts } from "../../../tokens";
import { PanelData, PanelSpec } from "./inspect";
import type { MapView } from "./viz/service-map";
import { ChartHintContext, compactHint } from "./chart-keyboard";
import { Viz } from "./viz";
import type { RowControls } from "./viz/table";
import { PanelTooltipContext } from "./panel-fullscreen";

export function PanelCard({ panel, title, result, loading, compare, range, height, group, editing, agentAvailable, annotations, vars, onSelect, onPoint, onVariable, onZoom, onRangePending, onZoomReset, onWiden, zoomed, onView, onCopyLink, onExplain, onFix, onRetry, onRemove, onDuplicate, onEdit, staleAt, traceLinks, controls, suspended = false, menuRef, initialView = "Chart" }: {
  initialView?: "Chart" | "Data" | "Spec";
  compare?: boolean; range?: string; panel: Panel; title: string; result?: PanelDisplayResult; loading: boolean; height: number; group: string; editing: boolean; agentAvailable: boolean;
  annotations?: AnnotationsResponse; vars?: Record<string, VarValue>; traceLinks?: "button"; controls?: RowControls;
  onSelect?: (value: string) => void; onView?(): void; onCopyLink?: () => void; onExplain?: () => void; onFix?: () => void; onRetry?(): void; onRemove?: () => void; onDuplicate?: () => void; staleAt?: number;
  suspended?: boolean; menuRef?: Ref<HTMLButtonElement>; onEdit?(): void;
  onVariable?: (name: string, value: string) => void; onPoint?: (selection: Selection) => void; onZoom?: (from: number, to: number) => void;
  zoomed?: boolean; onZoomReset?: () => void;
  /** Widens the page's time range; the empty state offers it only when given. */
  onWiden?: () => void; onRangePending?(pending: boolean): void;
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
  const [headerWidth, setHeaderWidth] = useState(0);
  useLayoutEffect(() => {
    const measure = () => {
      const context = document.createElement("canvas").getContext("2d");
      const titleFont = card.current?.querySelector("[data-panel-title]");
      const family = titleFont ? getComputedStyle(titleFont).fontFamily : "inherit";
      if (context) context.font = `600 15px ${family}`;
      const titleWidth = context?.measureText(title).width ?? Array.from(title).length * 8.25;
      if (context) context.font = `400 11px ${family}`;
      const switchWidth = ["Chart", "Data", "Spec"].reduce((sum, label) => sum + (context?.measureText(label).width ?? label.length * 7) + 12, 4);
      const extras = (panel.description ? 24 : 0) + 18 + (panelTimeLabel(panel)?.length ?? 0) * 7;
      setHeaderWidth(titleWidth + switchWidth + 22 + 32 + 20 + extras);
    };
    measure(); document.fonts?.addEventListener("loadingdone", measure);
    return () => document.fonts?.removeEventListener("loadingdone", measure);
  }, [title, panel.description, panel.time]);
  const compactViews = small || width > 0 && width < headerWidth;
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!staleAt) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, [staleAt]);
  const tableOnly = ["logs", "traces", "table", "log_patterns"].includes(panel.viz);
  const modes = tableOnly ? ["Table", "Data", "Spec"] : ["Chart", "Data", "Spec"];
  const [view, setView] = useState<string>(tableOnly && initialView === "Chart" ? "Table" : initialView);
  const [mapView, setMapView] = useState<MapView>();
  const onMapView = useCallback((next: MapView) => setMapView(next), []);
  const hint = panel.click ? `click to filter by ${panel.click.set_variable}`
    // A table row opens its own trace only when the row carries a trace_id; otherwise it lists exemplars.
    : panel.viz === "traces" || panel.viz === "table" && result?.frame?.columns.some(c => c.name === "trace_id") ? "click to open trace"
    // Which of the two a traces table does depends on its columns, so it says nothing until they arrive.
    : panel.viz === "table" && panel.drill === "traces" && !result?.frame ? undefined
    : panel.drill === "logs" ? "click for matching logs"
    : panel.drill === "traces" && ["table", "timeseries", "bar", "heatmap", "histogram", "scatter", "state_timeline"].includes(panel.viz) ? "click for exemplar traces" : undefined;
  const loadingBody = !suspended && !result && panel.viz !== "text" && (view === "Chart" || view === "Table");
  const bodyHeight = Math.max(40, height - 88);
  const constants = [...new Set(logConstants(panel, result?.frame).map(c => c.value))];
  const subtitle = `${panel.viz === "timeseries" ? "time series" : panel.viz.replaceAll("_", " ")}${constants.length ? ` · ${constants.join(" · ")}` : ""}${hint ? ` · ${hint}` : ""}`;
  const canvas = ["timeseries", "bar", "heatmap", "histogram", "scatter", "state_timeline", "gauge", "service_map"].includes(panel.viz);
  const scrolls = (view !== "Chart" && view !== "Table") || !canvas || !result || result.status !== "ok" || result.frame?.truncated || result.previous?.truncated;
  const rows = ["table", "logs", "traces", "log_patterns", "text"].includes(panel.viz);
  const note = formatPanelNote(result?.frame?.note);
  const refreshError = result?.frame && result.error;
  const retryable = Boolean(result?.request_error?.length);
  const fixable = Boolean(result?.error) && !retryable;
  const refreshAge = staleAt ? `Showing data from ${relativeTime(staleAt, now)}` : undefined;
  const refreshDetails = refreshError ? `${refreshAge ? `${refreshAge}. ` : ""}${refreshError}` : undefined;
  const refreshDescription = refreshDetails && refreshDetails.length > 200 ? `${refreshDetails.slice(0, 199)}…` : refreshDetails;
  const refreshDescriptionId = useId();

  const tooltipTarget = useRef<HTMLButtonElement>(null);
  const buttonRef = useMergedRef(menuRef, tooltipTarget);
  const [menuFocused, setMenuFocused] = useState(false);
  const [menuHovered, setMenuHovered] = useState(false);
  const [tooltipDismissed, setTooltipDismissed] = useState(false);
  const tooltipOpen = Boolean(refreshError) && !tooltipDismissed && (menuFocused || menuHovered);
  const onTooltipOpen = useContext(PanelTooltipContext);
  useEffect(() => {
    onTooltipOpen?.(tooltipOpen);
    return () => onTooltipOpen?.(false);
  }, [onTooltipOpen, tooltipOpen]);
  const closeTooltip = useCallback(() => {
    setTooltipDismissed(true);
    setMenuFocused(false);
    setMenuHovered(false);
  }, []);
  useEffect(() => {
    if (!tooltipOpen) return;
    const dismiss = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      closeTooltip();
    };
    document.addEventListener("keydown", dismiss, true);
    return () => document.removeEventListener("keydown", dismiss, true);
  }, [tooltipOpen, closeTooltip]);
  const notes = [...new Set([
    staleAt && !refreshError ? `Stale: last updated ${relativeTime(staleAt, now)}` : undefined,
    result?.frame?.truncated || result?.previous?.truncated ? "Truncated: showing limited data" : undefined,
    note, result?.annotation_error,
    // A count that matched nothing shows its 0; the diagnosis says why.
    result?.status === "ok" ? result.diagnosis : undefined,
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
  return <Paper ref={card} withBorder radius="md" h="100%" p={0} style={{ display: "flex", flexDirection: "column", minWidth: 0, position:"relative" }} data-panel={panel.id} data-compact-views={compactViews}>
    <Group justify="space-between" wrap="nowrap" gap="xs" px={16} pt={12} className={editing ? "panel-drag" : undefined} style={{ cursor: editing ? "grab" : undefined, flexShrink: 0 }}>
      <Group gap={6} wrap="nowrap" miw={0} style={{flex:1}}>
        <Box miw={0} style={{flex:1}}><Text data-panel-title fw={600} fz={14} truncate={compactViews ? undefined : true} style={compactViews ? {overflowWrap:"anywhere"} : undefined}>{title}</Text><Text data-panel-subtitle title={keyboardHint ?? subtitle} fz={12} ff={fonts.display} c="dimmed" truncate={small ? undefined : true} style={{position: "relative", ...(small ? {overflowWrap:"anywhere"} : {})}}>
            {/* Preserve the subtitle's measured line box, including narrow cards. */}
            <span aria-hidden={keyboardHint ? true : undefined} style={{visibility: keyboardHint ? "hidden" : undefined}}>{subtitle}</span>
            {keyboardHint && <><span data-chart-hint aria-hidden="true" style={{position: "absolute", inset: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap"}}>{compactHint(keyboardHint)}</span></>}
          </Text>
          {panel.options?.highlight && <Text component="span" data-highlight-term title={`highlight: ${panel.options.highlight}`} fz={12} c="dimmed" style={{display:"inline-block",maxWidth:"100%",overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"nowrap",border:"1px solid var(--mantine-color-default-border)",borderRadius:4,padding:"0 5px"}}>highlight: {panel.options.highlight}</Text>}
        </Box>
        {panelTimeLabel(panel) && <Text size="xs" c="dimmed" role="status">{panelTimeLabel(panel)}</Text>}
        {panel.description && <Tooltip label={panel.description} multiline w={260}><ActionIcon variant="transparent" color="gray" size="xs" aria-label={`${title} description`}><Info size={14} /></ActionIcon></Tooltip>}
        {loading && result && <Loader size={12} aria-label="Refreshing" />}
      </Group>
      <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
      {!compactViews && panel.viz !== "text" && <Button.Group aria-label={`${title} view`}>
        {modes.map(mode => <Button key={mode} size="compact-xs" variant="default" fw={view === mode ? 600 : 400} bg={view === mode ? "var(--mantine-color-accent-light)" : undefined} c={view === mode ? "var(--mantine-color-accent-text)" : undefined} data-panel-view={mode} aria-pressed={view === mode} onClick={() => setView(mode)}>{mode}</Button>)}
      </Button.Group>}
      {panel.viz === "service_map" && mapView?.canFit && <ActionIcon variant="subtle" color="gray" size="sm" aria-label={mapView.overview ? `Return to readable ${title} view` : `Show whole ${title} graph`} onClick={mapView.fit}><ArrowsOut size={16} /></ActionIcon>}
      {zoomed && onZoomReset && ["timeseries", "heatmap", "state_timeline"].includes(panel.viz) && <ActionIcon variant="subtle" color="gray" size="sm" aria-label={`Reset ${title} zoom`} onClick={onZoomReset}><ArrowCounterClockwise size={16} /></ActionIcon>}
      <Menu position="bottom-end" withinPortal onChange={opened => {if (opened) closeTooltip();}}>
        <Menu.Target><ActionIcon ref={buttonRef} variant="subtle" color="gray" size="sm" aria-label={`${title} menu${refreshDescription ? ", refresh failed" : ""}`} aria-describedby={refreshDescription ? refreshDescriptionId : undefined} onFocus={event => {
          const visible = event.currentTarget.matches(":focus-visible");
          setMenuFocused(visible);
          if (visible) setTooltipDismissed(false);
        }} onBlur={() => setMenuFocused(false)} onMouseEnter={() => {setMenuHovered(true); setTooltipDismissed(false);}} onMouseLeave={closeTooltip}>{refreshError ? <WarningCircle size={18} weight="fill" color="var(--mantine-color-warn-filled)" /> : <DotsThree size={18} weight="bold" />}</ActionIcon></Menu.Target>
        <Tooltip target={tooltipTarget} multiline maw="min(360px, calc(100vw - 32px))" label={<>
          {refreshAge && <Text size="xs">{refreshAge}</Text>}
          <Text data-refresh-error-message size="xs" title={refreshError || undefined} lineClamp={4} style={{overflowWrap:"anywhere"}}>{refreshError}</Text>
        </>} opened={tooltipOpen} />
        <Menu.Dropdown>
          {(compactViews || panel.viz === "text") && <>
            <Menu.RadioGroup value={view} onChange={setView}>
              <Box role="group" aria-label={`${title} view`}>
                {(panel.viz === "text" ? ["Chart", "Spec"] : modes).map(mode => <Menu.RadioItem key={mode} value={mode} data-panel-view={mode} closeMenuOnClick>{panel.viz === "text" && mode === "Chart" ? "Content" : mode}</Menu.RadioItem>)}
              </Box>
            </Menu.RadioGroup>
            <Menu.Divider />
          </>}
          {refreshError && retryable && onRetry && <Menu.Item disabled={loading} onClick={() => {closeTooltip(); onRetry();}}>Retry</Menu.Item>}
          {fixable && agentAvailable && onFix && <Menu.Item onClick={onFix}>Ask Fanout to fix it</Menu.Item>}
          {onView && <Menu.Item leftSection={<ArrowsOut size={14} />} onClick={onView}>View</Menu.Item>}
          {agentAvailable && onExplain && <Menu.Item leftSection={<ChatCircleText size={14} />} onClick={onExplain}>Explain in chat</Menu.Item>}
          {onCopyLink && <Menu.Item leftSection={<Copy size={14} />} onClick={onCopyLink}>Copy link</Menu.Item>}
          {onEdit && <Menu.Item onClick={onEdit}>Edit panel</Menu.Item>}
          {onDuplicate && <Menu.Item leftSection={<Copy size={14} />} onClick={onDuplicate}>Duplicate</Menu.Item>}
          {onRemove && <><Menu.Divider /><Menu.Item color="bad" leftSection={<Trash size={14} />} onClick={onRemove}>Remove panel</Menu.Item></>}
        </Menu.Dropdown>
      </Menu>
      </Group>
    </Group>
    {/* Data and Spec scroll static text, so the body takes focus for keyboard
        scrolling. It paints the card's own ground so the scrolled content has
        an opaque backdrop that spans it, which contrast checks need, and keeps
        the card's rounded bottom corners so that ground does not square them. */}
    <Box ref={body} data-panel-body data-panel-loading={loadingBody || undefined} className="dashboard-panel-padding" tabIndex={view !== "Chart" ? 0 : undefined} style={{ flex: "1 1 0px", isolation: "isolate", contain: rows || view === "Data" ? "paint" : undefined, minHeight: 0, minWidth: 0, padding: "16px", overflow: !loadingBody && scrolls ? "auto" : "hidden", display: !loadingBody && (rows || view !== "Chart") ? "block" : "flex", flexDirection: "column", backgroundColor: "inherit", borderBottomLeftRadius: "inherit", borderBottomRightRadius: "inherit" }}>
      {suspended ? <DataState state="empty" empty={{ title: "Shown in full-screen" }} /> : view === "Data" ? <PanelData panel={panel} result={result} /> : view === "Spec" ? <PanelSpec panel={panel} dark={dark} /> : !result && panel.viz !== "text" ? <DataState state="loading" loadingLabel={`Loading panel ${title || panel.id}`} skeleton={<LoadingSkeleton viz={panel.viz} />} />
        : (result?.status === "error" || result?.status === "invalid") ? <Box data-panel-error={result.reason !== "busy" || undefined} data-panel-busy={result.reason === "busy" || undefined} style={{minWidth: 0, maxHeight: "100%", overflow: "auto", overflowWrap: "anywhere"}}>
          <Notice live={false} tone={result.reason === "busy" ? "info" : "danger"} title={result.reason === "busy" ? "Not run yet" : result.status === "invalid" ? "This panel is invalid" : "This panel failed"} retryLabel="Retry" onRetry={retryable || result.reason === "busy" ? onRetry : undefined}>
            <Text data-panel-error-message={result.reason !== "busy" || undefined} title={result.error} size="xs" style={{overflowWrap: "anywhere"}}>{result.error || "The query could not run."}</Text>
            {result.problems?.map((problem, i) => <Text key={i} size="xs"><code>{problem.path}</code>{problem.message !== result.error && `: ${problem.message}`}{problem.hint && ` — ${problem.hint}`}</Text>)}
          </Notice>
          {fixable && agentAvailable && onFix && <Button size="compact-xs" variant="light" onClick={onFix}>Ask Fanout to fix it</Button>}
        </Box>
        : result?.status === "empty" ? <DataState state="empty" empty={{title: "No data in this time range", description: <Text size="sm" style={{overflowWrap: "anywhere"}}>{result.diagnosis || "Widen the time range to find matching telemetry."}</Text>, action: zoomed && onZoomReset ? <Button variant="default" size="compact-xs" onClick={onZoomReset}>Reset zoom</Button> : onWiden ? <Button variant="default" size="compact-xs" onClick={onWiden}>Widen time range</Button> : undefined}} />
        : <ChartHintContext.Provider value={chartHint}><Viz traceLinks={traceLinks} controls={controls} onMapView={onMapView} compare={compare} range={range} panel={panel} title={title} result={result} dark={dark} height={bodyHeight} group={group} annotations={annotations} vars={vars} onSelect={onSelect} onPoint={onPoint} onVariable={onVariable} onZoom={onZoom} onRangePending={onRangePending} /></ChartHintContext.Provider>}
    </Box>
    {notes.length > 0 && <Box data-panel-notes className="dashboard-panel-padding" pb={12} style={{ display: "flex", flexDirection: "column", gap: 4, flexShrink: 0, position: "relative", zIndex: 1, paddingTop: 8, background: "var(--mantine-color-body)", borderBottomLeftRadius: "inherit", borderBottomRightRadius: "inherit" }}>
      {notes.map(text => <Text key={text} data-panel-note fz={12} c="dimmed" role="status" title={text === note ? result?.frame?.note : undefined} style={{ overflowWrap: "anywhere" }}>{text}</Text>)}
    </Box>}
    {refreshDescription && <span id={refreshDescriptionId} hidden>{refreshDescription}</span>}
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
