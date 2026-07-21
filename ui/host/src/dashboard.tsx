import { ActionIcon, Badge, Box, Button, Center, Divider, Flex, Group, Indicator, Loader, Menu, Paper, ScrollArea, Select, SimpleGrid, Stack, Table, Text, TextInput, Title, Tooltip } from "@mantine/core";
import { ArrowClockwise, ArrowUpRight, CaretDown, Check, ListMagnifyingGlass, Plus, Sparkle, SquaresFour, X } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Responsive, WidthProvider } from "react-grid-layout/legacy";
import { authorizedFetch } from "./auth";
import { createID } from "./id";

const Grid = WidthProvider(Responsive);
const dashboardKey = "fanout.dashboard-id";
type WidgetType = "overview" | "topology" | "activity" | "assistant" | "performance" | "trace" | "logs";
type Widget = { id: string; type: WidgetType; title: string; config?: Record<string, unknown>; enabled: boolean };
type LayoutItem = { i: string; x: number; y: number; w: number; h: number; minW?: number; minH?: number };
type State = { layout: LayoutItem[]; widgets: Widget[]; filters: { window: string; namespace: string } };
type DashboardRecord = { id: string; name: string; description: string; is_default: boolean; state: State; updated_at: string };
type DashboardSummary = { id: string; name: string; description: string; is_default: boolean; widget_count: number; updated_at: string };
type Envelope<T> = { data: T };

async function getJSON<T>(url: string): Promise<T> {
  const response = await authorizedFetch(url);
  if (!response.ok) throw new Error(`Request failed (${response.status})`);
  return response.json();
}

const emptyState: State = { layout: [], widgets: [], filters: { window: "1h", namespace: "" } };
const widgetTitles: Record<WidgetType, string> = { overview: "System health", topology: "Service map", activity: "Recent activity", assistant: "Ask Fanout", performance: "Performance", trace: "Trace focus", logs: "Logs" };
const widgetMinimumRows: Record<WidgetType, number> = { overview: 4, topology: 4, activity: 4, assistant: 3, performance: 4, trace: 4, logs: 4 };

export default function Dashboard({ dashboardID = "", onOpenChat, onDashboardChange }: { dashboardID?: string; onOpenChat: (prompt?: string) => void; onDashboardChange?: (id: string, replace?: boolean) => void }) {
  const queryClient = useQueryClient();
  const dashboards = useQuery({ queryKey: ["dashboards"], queryFn: () => getJSON<{ dashboards: DashboardSummary[] }>("/api/dashboards"), refetchInterval: 3000 });
  const [selectedID, setSelectedID] = useState(() => dashboardID || localStorage.getItem(dashboardKey) || "");
  const selected = useQuery({ queryKey: ["dashboard", selectedID], queryFn: () => getJSON<DashboardRecord>(`/api/dashboards/${encodeURIComponent(selectedID)}`), enabled: Boolean(selectedID), refetchInterval: 3000 });
  const [state, setState] = useState<State>(emptyState);
  const [breakpoint, setBreakpoint] = useState("lg");

  useEffect(() => {
    if (!dashboardID || dashboardID === selectedID) return;
    setSelectedID(dashboardID);
    localStorage.setItem(dashboardKey, dashboardID);
  }, [dashboardID, selectedID]);
  useEffect(() => {
    const items = dashboards.data?.dashboards;
    if (!items?.length) return;
    if (dashboardID && items.some((item) => item.id === dashboardID)) return;
    if (!dashboardID && selectedID && items.some((item) => item.id === selectedID)) { onDashboardChange?.(selectedID, true); return; }
    const next = items.find((item) => item.is_default) ?? items[0];
    choose(next.id, true);
  }, [dashboardID, dashboards.data, selectedID]);
  useEffect(() => { if (selected.data?.state) setState(selected.data.state); }, [selected.data?.updated_at]);

  const save = useMutation({
    mutationFn: async (next: State) => {
      if (!selected.data) throw new Error("No dashboard selected");
      const response = await authorizedFetch(`/api/dashboards/${encodeURIComponent(selected.data.id)}`, { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify({ name: selected.data.name, description: selected.data.description, state: next }) });
      if (!response.ok) throw new Error("Unable to save dashboard");
      return response.json() as Promise<DashboardRecord>;
    },
    scope: { id: `dashboard-${selectedID}` },
    onSuccess: (data) => { queryClient.setQueryData(["dashboard", data.id], data); void queryClient.invalidateQueries({ queryKey: ["dashboards"] }); },
  });
  const layouts = useMemo(() => {
    const widgetType = new Map(state.widgets.map((widget) => [widget.id, widget.type]));
    const normalized = state.layout.map((item) => {
      const minimum = widgetMinimumRows[widgetType.get(item.i) ?? "overview"];
      return { ...item, h: Math.max(item.h, minimum), minH: Math.max(item.minH ?? 0, minimum) };
    });
    const compact = (columns: number) => normalized.map((item) => ({ ...item, x: 0, w: columns }));
    return { lg: normalized, md: normalized, sm: compact(6), xs: compact(2), xxs: compact(1) };
  }, [state.layout, state.widgets]);

  function choose(id: string, replace = false) { setSelectedID(id); localStorage.setItem(dashboardKey, id); onDashboardChange?.(id, replace); }
  function update(next: State) { setState(next); save.mutate(next); }
  function add(type: WidgetType) {
    const id = createID();
    const wide = ["topology", "performance", "trace", "logs"].includes(type);
    const minimumRows = widgetMinimumRows[type];
    update({ ...state, widgets: [...state.widgets, { id, type, title: widgetTitles[type], enabled: true }], layout: [...state.layout, { i: id, x: 0, y: Infinity, w: wide ? 8 : 4, h: minimumRows, minW: 3, minH: minimumRows }] });
  }
  function remove(id: string) { update({ ...state, widgets: state.widgets.filter((widget) => widget.id !== id), layout: state.layout.filter((item) => item.i !== id) }); }

  if (dashboards.isLoading || (selectedID && selected.isLoading)) return <LoadingState label="Loading your workspace…" />;
  if (dashboards.isError || selected.isError) return <LoadingState label="Your workspace is unavailable. Try refreshing." />;
  const item = selected.data;
  if (!item) return <LoadingState label="Preparing your workspace…" />;

  return <Box component="main" maw={1440} mx="auto" px={{ base: "md", sm: "xl", lg: 72 }} pt={{ base: "xl", sm: 52 }} pb={100}>
    <Flex justify="space-between" align={{ base: "flex-start", md: "flex-end" }} direction={{ base: "column", md: "row" }} gap="lg" mb="xl">
      <Box miw={0}>
        <Menu shadow="md" position="bottom-start" withinPortal>
          <Menu.Target><Button variant="subtle" color="gray" size="compact-sm" leftSection={<SquaresFour size={16} weight="fill" />} rightSection={<CaretDown size={13} weight="bold" />}>Dashboards</Button></Menu.Target>
          <Menu.Dropdown><Menu.Label>Switch dashboard</Menu.Label>{(dashboards.data?.dashboards ?? []).map((dashboard) => <Menu.Item key={dashboard.id} leftSection={dashboard.id === selectedID ? <Check size={14} weight="bold" /> : <Box w={14} />} onClick={() => choose(dashboard.id)}>{dashboard.name}</Menu.Item>)}</Menu.Dropdown>
        </Menu>
        <Title order={1} fz={{ base: 36, sm: 52 }} lts="-0.045em" mt={4}>{item.name}</Title>
        <Text c="dimmed" mt={4}>{item.description || "A focused view of the signals that matter now."}</Text>
      </Box>
      <Group wrap="nowrap" w={{ base: "100%", md: "auto" }}>
        <Button variant="default" leftSection={<Sparkle size={16} weight="fill" />} flex={{ base: 1, md: "initial" }} onClick={() => onOpenChat("Create a new dashboard for me. First ask what I want to monitor, then design it when you have enough context.")}>Create with AI</Button>
        <Button leftSection={selected.isFetching ? <Loader size={15} color="white" /> : <ArrowClockwise size={16} weight="bold" />} onClick={() => void queryClient.invalidateQueries()}>{selected.isFetching ? "Refreshing" : "Refresh"}</Button>
      </Group>
    </Flex>

    <Paper withBorder radius="lg" p="lg" mb="lg" role="group" aria-label="Dashboard controls">
      <Flex align={{ base: "stretch", md: "flex-end" }} justify="space-between" direction={{ base: "column", md: "row" }} gap="md">
        <Group align="flex-end" gap="md" grow wrap="wrap" w={{ base: "100%", md: "auto" }}>
          <Select label="Window" value={state.filters.window} onChange={(window) => window && update({ ...state, filters: { ...state.filters, window } })} data={[{ value: "15m", label: "15 minutes" }, { value: "1h", label: "1 hour" }, { value: "6h", label: "6 hours" }, { value: "24h", label: "24 hours" }]} w={{ base: "100%", xs: 150 }} />
          <TextInput label="Namespace" value={state.filters.namespace} onChange={(event) => setState({ ...state, filters: { ...state.filters, namespace: event.currentTarget.value } })} onBlur={() => update(state)} placeholder="All namespaces" w={{ base: "100%", xs: 220 }} />
        </Group>
        <Flex wrap="nowrap" justify={{ base: "space-between", md: "flex-end" }} align="center" gap="md" w={{ base: "100%", md: "auto" }}>
          <Group gap="xs" wrap="nowrap"><Indicator color={save.isError ? "red" : save.isPending ? "yellow" : "teal"} processing={save.isPending} size={8} /><Text c="dimmed" size="sm" miw={48}>{save.isPending ? "Saving" : save.isError ? "Failed" : "Saved"}</Text></Group>
          <Divider orientation="vertical" h={28} />
          <Menu shadow="md" position="bottom-end" withinPortal>
            <Menu.Target><Button variant="default" leftSection={<Plus size={16} weight="bold" />} rightSection={<CaretDown size={14} weight="bold" />}>Add view</Button></Menu.Target>
            <Menu.Dropdown><Menu.Label>Dashboard views</Menu.Label>{Object.entries(widgetTitles).map(([value, label]) => <Menu.Item key={value} onClick={() => add(value as WidgetType)}>{label}</Menu.Item>)}</Menu.Dropdown>
          </Menu>
          <Button variant="subtle" color="gray" rightSection={<ArrowUpRight size={16} weight="bold" />} onClick={() => onOpenChat()}>Ask Fanout</Button>
        </Flex>
      </Flex>
    </Paper>

    <Grid className="dashboard-grid" layouts={layouts} breakpoints={{ lg: 1100, md: 800, sm: 600, xs: 420, xxs: 0 }} cols={{ lg: 12, md: 10, sm: 6, xs: 2, xxs: 1 }} rowHeight={76} margin={[16, 16]} containerPadding={[0, 0]} compactType="vertical" draggableCancel="button,input,select,textarea,a,label,[role=menu]" onBreakpointChange={setBreakpoint} onDragStop={(layout: readonly LayoutItem[]) => { if (breakpoint === "lg") update({ ...state, layout: [...layout] }); }} onResizeStop={(layout: readonly LayoutItem[]) => { if (breakpoint === "lg") update({ ...state, layout: [...layout] }); }}>
      {state.widgets.map((widget) => <div key={widget.id}><WidgetCard widget={widget} filters={state.filters} onRemove={() => remove(widget.id)} onOpenChat={onOpenChat} /></div>)}
    </Grid>
  </Box>;
}

function LoadingState({ label }: { label: string }) {
  return <Center mih="50vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">{label}</Text></Center>;
}

function WidgetCard({ widget, filters, onRemove, onOpenChat }: { widget: Widget; filters: State["filters"]; onRemove: () => void; onOpenChat: (prompt?: string) => void }) {
  const base = new URLSearchParams({ window: filters.window, limit: "40" });
  if (filters.namespace) base.set("namespace", filters.namespace);
  const service = typeof widget.config?.service === "string" ? widget.config.service : "";
  if (service) base.set("service", service);
  const overview = useQuery({ queryKey: ["overview", base.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/overview?${base}`), enabled: widget.type === "overview" || widget.type === "activity" });
  const topology = useQuery({ queryKey: ["topology", base.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/topology?${base}`), enabled: widget.type === "topology" });
  const performance = useQuery({ queryKey: ["performance", base.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/performance?${base}`), enabled: widget.type === "performance" });
  const logQuery = new URLSearchParams(base); if (typeof widget.config?.severity === "string") logQuery.set("severity", widget.config.severity); if (typeof widget.config?.search === "string") logQuery.set("search", widget.config.search);
  const logs = useQuery({ queryKey: ["logs", logQuery.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/logs?${logQuery}`), enabled: widget.type === "logs" });
  const traceQuery = new URLSearchParams(base); if (typeof widget.config?.trace_id === "string") traceQuery.set("trace_id", widget.config.trace_id);
  const trace = useQuery({ queryKey: ["trace", traceQuery.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/trace?${traceQuery}`), enabled: widget.type === "trace" });
  const health = overview.data?.data;

  return <Paper withBorder shadow="xs" radius="lg" p="lg" h="100%" style={{ overflow: "hidden" }}><Stack h="100%" gap="sm">
    <Group justify="space-between" align="flex-start" wrap="nowrap"><Box><Text c="dimmed" size="xs" fw={700} tt="uppercase" lts="0.1em">{widget.type === "assistant" ? "Guidance" : widget.type}</Text><Title order={2} fz="lg" mt={2}>{widget.title}</Title></Box><Tooltip label={`Remove ${widget.title}`}><ActionIcon variant="subtle" color="red" aria-label={`Remove ${widget.title}`} onClick={onRemove}><X size={16} weight="bold" /></ActionIcon></Tooltip></Group>
    <ScrollArea type="auto" offsetScrollbars flex={1}>
      {widget.type === "overview" && <SimpleGrid cols={2} spacing="sm"><Metric label="Health" value={health?.health ?? "—"} /><Metric label="Services" value={health?.service_count ?? "—"} /><Metric label="Spans" value={health?.total_spans?.toLocaleString?.() ?? "—"} /><Metric label="Error rate" value={health ? `${(health.error_rate * 100).toFixed(2)}%` : "—"} /></SimpleGrid>}
      {widget.type === "topology" && <DataTable rows={(topology.data?.data?.nodes ?? []).slice(0, 6).map((node: any) => [<HealthBadge key="health" health={node.health} label={node.service} />, `${node.spans?.toLocaleString?.() ?? 0} spans`, `${node.p95_ms?.toFixed?.(1) ?? "—"} ms p95`])} empty="No service relationships in this window" />}
      {widget.type === "activity" && <DataTable rows={(health?.services ?? []).slice(0, 5).map((entry: any) => [<HealthBadge key="health" health={entry.health} label={entry.service} />, entry.error_rate ? `${(entry.error_rate * 100).toFixed(2)}% errors` : "Operating normally"])} empty="No recent activity" />}
      {widget.type === "performance" && <DataTable rows={(performance.data?.data?.endpoints ?? []).slice(0, 5).map((endpoint: any) => [<Text key="path" fw={600} size="sm" truncate>{endpoint.method} {endpoint.path}</Text>, `${endpoint.calls?.toLocaleString?.()} calls`, `${endpoint.p95_ms?.toFixed?.(1)} ms p95`])} empty="No endpoint activity in this window" />}
      {widget.type === "logs" && <DataTable rows={(logs.data?.data?.entries ?? []).slice(0, 5).map((entry: any) => [<Badge key="severity" color={severityColor(entry.severity)} variant="light">{entry.severity}</Badge>, entry.service, entry.body])} empty="No matching logs in this window" />}
      {widget.type === "trace" && <Stack><SimpleGrid cols={2} spacing="sm"><Metric label="Duration" value={trace.data?.data ? `${trace.data.data.duration_ms.toFixed?.(1)} ms` : "—"} /><Metric label="Spans" value={trace.data?.data?.spans?.length ?? "—"} /><Metric label="Services" value={trace.data?.data?.services?.length ?? "—"} /><Metric label="Status" value={trace.data?.data ? trace.data.data.has_error ? "Error" : "Healthy" : "—"} /></SimpleGrid><Text c="dimmed" size="xs" ff="monospace" truncate>{trace.data?.data?.trace_id ? `Trace ${trace.data.data.trace_id}` : "Most relevant recent trace"}</Text></Stack>}
      {widget.type === "assistant" && <Stack align="flex-start"><Text c="dimmed">Ask a focused question about health, latency, errors, or dependencies.</Text><Button leftSection={<Sparkle size={16} weight="fill" />} onClick={() => onOpenChat("Summarize the most important system changes in the selected window")}>Start a conversation</Button></Stack>}
    </ScrollArea>
  </Stack></Paper>;
}

function DataTable({ rows, empty }: { rows: React.ReactNode[][]; empty: string }) {
  if (!rows.length) return <Empty text={empty} />;
  return <Table.ScrollContainer minWidth={420}><Table verticalSpacing="sm" highlightOnHover>{<Table.Tbody>{rows.map((cells, rowIndex) => <Table.Tr key={rowIndex}>{cells.map((cell, cellIndex) => <Table.Td key={cellIndex}><Text component="span" size="sm" c={cellIndex ? "dimmed" : undefined} lineClamp={1}>{cell}</Text></Table.Td>)}</Table.Tr>)}</Table.Tbody>}</Table></Table.ScrollContainer>;
}

function HealthBadge({ health, label }: { health: string; label: string }) {
  return <Badge color={health === "healthy" ? "teal" : health === "degraded" ? "yellow" : "red"} variant="light" tt="none">{label}</Badge>;
}

function severityColor(severity: string) {
  const value = String(severity).toUpperCase();
  return value === "ERROR" || value === "FATAL" ? "red" : value === "WARN" || value === "WARNING" ? "yellow" : value === "INFO" ? "teal" : "blue";
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return <Paper withBorder radius="md" p="sm" bg="gray.0"><Text c="dimmed" size="xs">{label}</Text><Text fw={700} fz="xl" mt={4} tt="capitalize">{value}</Text></Paper>;
}

function Empty({ text }: { text: string }) {
  return <Center py="xl"><ListMagnifyingGlass size={20} /><Text c="dimmed" size="sm" ml="xs">{text}</Text></Center>;
}
