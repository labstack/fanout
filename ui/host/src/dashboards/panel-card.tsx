import { ActionIcon, Box, Button, Center, Group, Loader, Menu, Paper, Stack, Text, Tooltip, useComputedColorScheme } from "@mantine/core";
import { ArrowsOut, ArrowCounterClockwise, ChatCircleText, Copy, DotsThree, Info, ListMagnifyingGlass, Trash, WarningCircle } from "@phosphor-icons/react";
import type { Panel, PanelResult, Selection, VarValue } from "../../../panels/types";
import type { AnnotationsResponse } from "../../../panels/annotations";
import { panelTimeLabel } from "../../../panels/interaction";
import { useCallback, useEffect, useState } from "react";
import { fonts } from "../../../tokens";
import { PanelData, PanelSpec } from "./inspect";
import type { MapView } from "./viz/service-map";
import { Viz } from "./viz";

export function PanelCard({ panel, title, result, loading, compare, range, height, group, editing, agentAvailable, annotations, vars, onSelect, onPoint, onVariable, onZoom, onZoomReset, zoomed, onView, onCopyLink, onExplain, onRemove, onDuplicate, staleAt }: {
  compare?: boolean; range?: string; panel: Panel; title: string; result?: PanelResult; loading: boolean; height: number; group: string; editing: boolean; agentAvailable: boolean;
  annotations?: AnnotationsResponse; vars?: Record<string, VarValue>;
  onSelect?: (value: string) => void; onView(): void; onCopyLink(): void; onExplain(): void; onRemove?: () => void; onDuplicate?: () => void; staleAt?: number;
  onVariable?: (name: string, value: string) => void; onPoint?: (selection: Selection) => void; onZoom?: (from: number, to: number) => void;
  zoomed?: boolean; onZoomReset?: () => void;
}) {
  const dark = useComputedColorScheme("light") === "dark";
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
  const hint = panel.click && panel.viz === "bar" ? `Click a bar to filter the board by ${panel.click.set_variable}.`
    : panel.drill && ["timeseries", "heatmap", "state_timeline"].includes(panel.viz) ? "Click the chart for exemplar traces at that time."
    : (panel.drill === "traces" || result?.frame?.columns.some(c => c.name === "trace_id")) && ["table", "traces"].includes(panel.viz) ? "Click a row to open the trace." : undefined;
  const bodyHeight = Math.max(40, height - 88 - (hint ? 28 : 0));
  const subtitle = `${panel.viz === "timeseries" ? "time series" : panel.viz.replaceAll("_", " ")} · ${panel.sql ? "sql" : panel.query?.from ?? (panel.viz === "service_map" || panel.viz === "health" ? "spans" : panel.viz === "log_patterns" ? "logs" : "spec")}`;
  const canvas = ["timeseries", "bar", "heatmap", "histogram", "scatter", "state_timeline", "gauge", "service_map"].includes(panel.viz);
  const scrolls = view !== "Chart" || !canvas || !result || result.status !== "ok" || result.frame?.truncated || result.previous?.truncated;
  const rows = ["table", "logs", "traces", "log_patterns", "text"].includes(panel.viz);
  const note = formatPanelNote(result?.frame?.note);
  return <Paper withBorder radius="md" h="100%" p={0} style={{ display: "flex", flexDirection: "column", minWidth: 0 }} data-panel={panel.id}>
    <Group justify="space-between" wrap="nowrap" gap="xs" px={16} pt={12} className={editing ? "panel-drag" : undefined} style={{ cursor: editing ? "grab" : undefined, flexShrink: 0 }}>
      <Group gap={6} wrap="nowrap" miw={0}>
        <Box miw={0}><Text data-panel-title fw={600} fz={15} truncate>{title}</Text><Text data-panel-subtitle fz={12} ff={fonts.display} c="dimmed" truncate>{subtitle}</Text></Box>
        {panelTimeLabel(panel) && <Text size="xs" c="dimmed" role="status">{panelTimeLabel(panel)}</Text>}
        {panel.description && <Tooltip label={panel.description} multiline w={260}><ActionIcon variant="transparent" color="gray" size="xs" aria-label={`${title} description`}><Info size={14} /></ActionIcon></Tooltip>}
        {loading && result && <Loader size={12} aria-label="Refreshing" />}
      </Group>
      <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
      <Group gap={0} wrap="nowrap" role="group" aria-label={`${title} view`} style={{ border: "1px solid var(--mantine-color-default-border)", borderRadius: 5, overflow: "hidden" }}>
        {["Chart", "Data", "Spec"].map(mode => <button key={mode} type="button" data-panel-view={mode} aria-pressed={view === mode} onClick={() => setView(mode)} style={{ border: 0, borderLeft: mode === "Chart" ? undefined : "1px solid var(--mantine-color-default-border)", padding: "2px 6px", fontSize: 11, fontFamily: "inherit", cursor: "pointer", background: view === mode ? "var(--mantine-color-default)" : "transparent", color: view === mode ? "var(--mantine-primary-color-filled)" : "var(--mantine-color-dimmed)", fontWeight: view === mode ? 600 : 400, boxShadow: view === mode ? "inset 0 -2px var(--mantine-primary-color-filled)" : undefined }}>{mode}</button>)}
      </Group>
      {panel.viz === "service_map" && mapView?.zoomed && <ActionIcon variant="subtle" color="gray" size="sm" aria-label={`Fit ${title} graph`} onClick={mapView.fit}><ArrowsOut size={16} /></ActionIcon>}
      {zoomed && onZoomReset && ["timeseries", "heatmap", "state_timeline"].includes(panel.viz) && <ActionIcon variant="subtle" color="gray" size="sm" aria-label={`Reset ${title} zoom`} onClick={onZoomReset}><ArrowCounterClockwise size={16} /></ActionIcon>}
      <Menu position="bottom-end" withinPortal>
        <Menu.Target><ActionIcon variant="subtle" color="gray" size="sm" aria-label={`${title} menu`}><DotsThree size={18} weight="bold" /></ActionIcon></Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<ArrowsOut size={14} />} onClick={onView}>View</Menu.Item>
          {agentAvailable && <Menu.Item leftSection={<ChatCircleText size={14} />} onClick={onExplain}>Explain in chat</Menu.Item>}
          <Menu.Item leftSection={<Copy size={14} />} onClick={onCopyLink}>Copy link</Menu.Item>
          {onDuplicate && <Menu.Item leftSection={<Copy size={14} />} onClick={onDuplicate}>Duplicate</Menu.Item>}
          {onRemove && <><Menu.Divider /><Menu.Item color="bad" leftSection={<Trash size={14} />} onClick={onRemove}>Remove panel</Menu.Item></>}
        </Menu.Dropdown>
      </Menu>
      </Group>
    </Group>
    <Box data-panel-body style={{ flex: 1, minHeight: 0, minWidth: 0, padding: "16px", overflow: scrolls ? "auto" : "hidden", display: rows || view !== "Chart" ? "block" : "flex", flexDirection: "column" }}>
      {view === "Data" ? <PanelData panel={panel} result={result} /> : view === "Spec" ? <PanelSpec panel={panel} dark={dark} /> : !result && panel.viz !== "text" ? <Center style={{ minHeight: "100%", flexShrink: 0 }}><Loader size="sm" aria-label="Loading panel" /></Center>
        : result?.status === "error" ? <Center style={{ minHeight: "100%", flexShrink: 0 }}><Stack align="center" gap={4} maw={420}>
          <Group gap={6}><WarningCircle size={18} weight="fill" color="var(--mantine-color-bad-filled)" /><Text size="sm" fw={500} c="bad">This panel failed</Text></Group>
          <Text size="xs" c="dimmed" ta="center" style={{ overflowWrap: "anywhere" }}>{result.error}</Text>
          {agentAvailable && <Button size="compact-xs" variant="light" onClick={onExplain}>Ask Fanout to fix it</Button>}
        </Stack></Center>
        : result?.status === "empty" ? <Center style={{ minHeight: "100%", flexShrink: 0 }}><Stack align="center" gap={4} maw={420}>
          <ListMagnifyingGlass size={20} color="var(--mantine-color-dimmed)" />
          <Text size="sm" c="dimmed" ta="center">{result.diagnosis || "No data in this time range."}</Text>
        </Stack></Center>
        : <Viz onMapView={onMapView} compare={compare} range={range} panel={panel} title={title} result={result} dark={dark} height={bodyHeight} group={group} annotations={annotations} vars={vars} onSelect={onSelect} onPoint={onPoint} onVariable={onVariable} onZoom={onZoom} />}
    </Box>
    {view === "Chart" && hint && <Text data-panel-hint fz={12} c="dimmed" px={16} pb={12} style={{ flexShrink: 0 }}>{hint}</Text>}
    {(staleAt || result?.frame?.truncated || result?.previous?.truncated) && <Text size="xs" c={staleAt ? "warn" : "dimmed"} mt={6} role="status" style={{ flexShrink: 0 }}>
      {staleAt ? `Stale: last updated ${relativeTime(staleAt, now)}` : ""}
      {(result?.frame?.truncated || result?.previous?.truncated) ? `${staleAt ? " · " : ""}Truncated: showing limited data` : ""}
    </Text>}
    {note && <Text size="xs" c="dimmed" role="status" title={result?.frame?.note} style={{ flexShrink: 0, overflowWrap: "anywhere" }}>{note}</Text>}
    {result?.annotation_error && <Text size="xs" c="warn" role="status" style={{ flexShrink: 0 }}>{result.annotation_error}</Text>}
    {result?.annotation_scope?.limited && <Text size="xs" c="warn" role="status" style={{ flexShrink: 0 }}>Annotation service scope is limited.</Text>}
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
