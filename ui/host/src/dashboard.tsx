import { Alert, Box, Button, Center, Flex, Group, Loader, Menu, Select, Stack, Text, TextInput, Title, useComputedColorScheme } from "@mantine/core";
import { ArrowUpRight, CaretDown, Plus, WarningCircle } from "@phosphor-icons/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { Responsive, WidthProvider } from "react-grid-layout/legacy";
import { dashboardsQueryKey, getJSON, type DashboardRecord, type DashboardState, type DashboardSummary } from "./api";
import type { Overview } from "../../contracts";
import { authorizedFetch } from "./auth";
import { compactDashboardLayout, nextDashboardSlot, widgetDefaults, widgetTypes, type DashboardLayoutItem, type WidgetType } from "./dashboard-layout";
import { createID } from "./id";
import { dashboardWindows, useLastUpdated, useObservability, widgetParams, type WidgetConfig } from "./widgets/data";
import WidgetCard, { widgetTitles, type Widget } from "./widgets/widget-card";

const Grid = WidthProvider(Responsive);
const dashboardKey = "fanout.dashboard-id";
const emptyState: DashboardState = { layout: [], widgets: [], filters: { window: "1h", namespace: "" } };

export default function Dashboard({ dashboardID = "", agentAvailable, onOpenChat, onDashboardChange }: { dashboardID?: string; agentAvailable: boolean; onOpenChat: (prompt?: string) => void; onDashboardChange?: (id: string, replace?: boolean) => void }) {
  const queryClient = useQueryClient();
  const dark = useComputedColorScheme("light") === "dark";
  const dashboards = useQuery({ queryKey: dashboardsQueryKey, queryFn: () => getJSON<{ dashboards: DashboardSummary[] }>("/api/dashboards"), refetchInterval: 30_000 });
  const [selectedID, setSelectedID] = useState(() => dashboardID || localStorage.getItem(dashboardKey) || "");
  const save = useMutation({
    mutationFn: async (next: DashboardState) => {
      if (!selected.data) throw new Error("No dashboard selected");
      const response = await authorizedFetch(`/api/dashboards/${encodeURIComponent(selected.data.id)}`, { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify({ name: selected.data.name, description: selected.data.description, state: next }) });
      if (!response.ok) throw new Error("Unable to save dashboard");
      return response.json() as Promise<DashboardRecord>;
    },
    scope: { id: `dashboard-${selectedID}` },
    onSuccess: (data) => { queryClient.setQueryData(["dashboard", data.id], data); void queryClient.invalidateQueries({ queryKey: dashboardsQueryKey }); },
    onError: (cause) => console.error("Dashboard save failed", cause),
  });
  // Pause polling while a save is in flight or failing so the refetch cannot clobber unsaved local edits.
  const selected = useQuery({ queryKey: ["dashboard", selectedID], queryFn: () => getJSON<DashboardRecord>(`/api/dashboards/${encodeURIComponent(selectedID)}`), enabled: Boolean(selectedID), refetchInterval: save.isPending || save.isError ? false : 30_000 });
  const [state, setState] = useState<DashboardState>(emptyState);
  const [breakpoint, setBreakpoint] = useState("lg");
  const overview = useObservability<Overview>("overview", widgetParams(state.filters), Boolean(selected.data));
  const services = useMemo(() => (overview.data?.data.services ?? []).map((service) => service.service), [overview.data]);
  const updatedAt = useLastUpdated();

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
  useEffect(() => {
    if (save.isPending || save.isError) return;
    if (selected.data?.state) setState(selected.data.state);
  }, [selected.data?.updated_at, save.isPending, save.isError]);
  // A failed save must not follow the user to another dashboard: reset the
  // mutation on switch so state sync and polling resume for the new
  // selection, and a retry can never write the previous dashboard's layout
  // into the newly selected one.
  useEffect(() => { save.reset(); }, [selectedID]);

  const layouts = useMemo(() => {
    const widgetType = new Map(state.widgets.map((widget) => [widget.id, widget.type as WidgetType]));
    const normalized: DashboardLayoutItem[] = state.layout.map((item) => {
      // A saved dashboard can name a widget type this build does not know,
      // and an unknown key has no default size to read minimums from.
      const size = widgetDefaults[widgetType.get(item.i) ?? "overview"] ?? widgetDefaults.overview;
      return { ...item, h: Math.max(item.h, size.minH), minW: Math.max(item.minW ?? 0, size.minW), minH: Math.max(item.minH ?? 0, size.minH) };
    });
    return { lg: normalized, md: normalized, sm: compactDashboardLayout(normalized, 6), xs: compactDashboardLayout(normalized, 2), xxs: compactDashboardLayout(normalized, 1) };
  }, [state.layout, state.widgets]);

  function choose(id: string, replace = false) { setSelectedID(id); localStorage.setItem(dashboardKey, id); onDashboardChange?.(id, replace); }
  function update(next: DashboardState) { setState(next); save.mutate(next); }
  function add(type: WidgetType) {
    const id = createID();
    const size = widgetDefaults[type];
    const slot = nextDashboardSlot(state.layout, size.w, size.h, 12, size.minW);
    update({ ...state, widgets: [...state.widgets, { id, type, title: widgetTitles[type], enabled: true }], layout: [...state.layout, { i: id, x: slot.x, y: slot.y, w: slot.w, h: size.h, minW: size.minW, minH: size.minH }] });
  }
  function remove(id: string) { update({ ...state, widgets: state.widgets.filter((widget) => widget.id !== id), layout: state.layout.filter((item) => item.i !== id) }); }
  function configure(id: string, config: WidgetConfig) { update({ ...state, widgets: state.widgets.map((widget) => (widget.id === id ? { ...widget, config } : widget)) }); }

  if (dashboards.isLoading || (selectedID && selected.isLoading)) return <LoadingState label="Loading your dashboard…" />;
  if (dashboards.isError || selected.isError) return <LoadingState label="Your dashboard is unavailable. Try refreshing." />;
  const item = selected.data;
  if (!item) return <LoadingState label="Preparing your dashboard…" />;

  return <Box component="main" maw={1440} mx="auto" px={{ base: "md", sm: "xl" }} pt={{ base: "lg", sm: "xl" }} pb="xl">
    <Stack gap="md" mb="lg">
      <Box>
        <Title order={1} fz={32} lts="-0.03em">{item.name}</Title>
        <Text c="dimmed" mt={2}>{item.description || "A focused view of the signals that matter now."}</Text>
      </Box>
      <Flex align={{ base: "stretch", md: "center" }} justify="space-between" direction={{ base: "column", md: "row" }} gap="sm" role="group" aria-label="Dashboard controls">
        <Group gap="sm" wrap="wrap">
          <Select aria-label="Window" value={state.filters.window} onChange={(window) => window && update({ ...state, filters: { ...state.filters, window } })} data={dashboardWindows} w={{ base: "100%", xs: 150 }} size="sm" />
          <TextInput aria-label="Namespace" value={state.filters.namespace} onChange={(event) => setState({ ...state, filters: { ...state.filters, namespace: event.currentTarget.value } })} onBlur={(event) => update({ ...state, filters: { ...state.filters, namespace: event.currentTarget.value } })} placeholder="All namespaces" w={{ base: "100%", xs: 200 }} size="sm" />
        </Group>
        {/* The row wraps rather than squeezing: at 390px a single line clipped
            both button labels to "Add vie" and "Ask Fano". */}
        <Group gap="sm" wrap="wrap" justify="flex-end">
          {updatedAt && <Text c="dimmed" size="xs">Updated {new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(updatedAt))}</Text>}
          <Menu shadow="md" position="bottom-end" withinPortal>
            <Menu.Target><Button variant="default" size="sm" leftSection={<Plus size={15} weight="bold" />} rightSection={<CaretDown size={13} weight="bold" />}>Add view</Button></Menu.Target>
            <Menu.Dropdown>{widgetTypes.filter((type) => agentAvailable || type !== "assistant").map((type) => <Menu.Item key={type} onClick={() => add(type)}>{widgetTitles[type]}</Menu.Item>)}</Menu.Dropdown>
          </Menu>
          {agentAvailable && <Button variant="subtle" color="gray" size="sm" rightSection={<ArrowUpRight size={15} weight="bold" />} onClick={() => onOpenChat()}>Ask Fanout</Button>}
        </Group>
      </Flex>
    </Stack>

    {save.isError && <Alert color="bad" radius="lg" mb="lg" icon={<WarningCircle size={18} weight="fill" />} title="Dashboard changes not saved">
      <Group justify="space-between" gap="sm">
        <Text size="sm">Your latest edits are kept on this screen but Fanout could not store them.</Text>
        <Button size="compact-sm" color="bad" variant="light" onClick={() => save.mutate(state)}>Retry save</Button>
      </Group>
    </Alert>}

    <Grid className="dashboard-grid" layouts={layouts} breakpoints={{ lg: 1100, md: 800, sm: 600, xs: 420, xxs: 0 }} cols={{ lg: 12, md: 10, sm: 6, xs: 2, xxs: 1 }} rowHeight={76} margin={[16, 16]} containerPadding={[0, 0]} compactType="vertical" draggableHandle=".widget-drag" draggableCancel="button,input,select,textarea,a,label,[role=menu],[role=dialog],.widget-actions" onBreakpointChange={setBreakpoint} onDragStop={(layout: readonly DashboardLayoutItem[]) => { if (breakpoint === "lg") update({ ...state, layout: [...layout] }); }} onResizeStop={(layout: readonly DashboardLayoutItem[]) => { if (breakpoint === "lg") update({ ...state, layout: [...layout] }); }}>
      {state.widgets.map((widget) => <div key={widget.id}>
        <WidgetCard widget={widget as Widget} filters={state.filters} dark={dark} services={services} agentAvailable={agentAvailable} onOpenChat={onOpenChat} onRemove={() => remove(widget.id)} onConfigure={(config) => configure(widget.id, config)} />
      </div>)}
    </Grid>
  </Box>;
}

function LoadingState({ label }: { label: string }) {
  return <Center mih="50vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">{label}</Text></Center>;
}
