import { ActionIcon, Button, Group, Menu, Modal, Paper, Stack, Text, Title } from "@mantine/core";
import { DotsThree, SlidersHorizontal, Trash } from "@phosphor-icons/react";
import { useState, type JSX } from "react";
import type { DashboardWidgetRecord } from "../api";
import type { WidgetType } from "../dashboard-layout";
import ActivityWidget from "./activity";
import AssistantWidget from "./assistant";
import { ConfigureWidget, configurable } from "./configure";
import type { Filters, WidgetConfig } from "./data";
import LogsWidget from "./logs";
import OverviewWidget from "./overview";
import PerformanceWidget from "./performance";
import { Empty } from "./pieces";
import TopologyWidget from "./topology";
import TraceWidget from "./trace";

// The server is the source of widget types, so a stored dashboard's widget
// can name one this build does not know. Widget mirrors that truth (type is
// a plain string) rather than the narrower WidgetType this build handles —
// DashboardWidgetRecord already says exactly this, so Widget just aliases it.
export type Widget = DashboardWidgetRecord;

export type WidgetBodyProps = {
  widget: Widget;
  filters: Filters;
  dark: boolean;
  services: string[];
  agentAvailable: boolean;
  onOpenChat: (prompt?: string) => void;
};

export const widgetTitles: Record<WidgetType, string> = { overview: "System health", topology: "Service map", activity: "Recent activity", assistant: "Ask Fanout", performance: "Performance", trace: "Trace focus", logs: "Logs" };

const bodies: Record<WidgetType, (props: WidgetBodyProps) => JSX.Element> = { overview: OverviewWidget, topology: TopologyWidget, activity: ActivityWidget, assistant: AssistantWidget, performance: PerformanceWidget, trace: TraceWidget, logs: LogsWidget };

function UnknownWidget() {
  return <Empty text="This view type is not supported by this version" />;
}

/* The server is the source of widget types, so a stored dashboard can name
   one this build does not know. The map stays exhaustive over WidgetType,
   which keeps a new type a compile error, and the guard is what proves the
   narrowing at runtime. */
function bodyFor(type: string) {
  return Object.hasOwn(bodies, type) ? bodies[type as WidgetType] : UnknownWidget;
}

export default function WidgetCard(props: WidgetBodyProps & { onRemove: () => void; onConfigure: (config: WidgetConfig) => void }) {
  const { widget, services, onRemove, onConfigure } = props;
  const [menuOpened, setMenuOpened] = useState(false);
  const [configuring, setConfiguring] = useState(false);
  // Remove used to take effect on the click, and a dashboard has no undo: the
  // card, its size and its configuration were gone, and rebuilding one that
  // the assistant had composed meant asking for it again.
  const [confirmingRemove, setConfirmingRemove] = useState(false);
  // A dashboard saved by a newer build can name a view this one has never
  // heard of. That is one card that says so, not a crash that takes the
  // whole page down with it.
  const Body = bodyFor(widget.type);
  return <Paper withBorder radius="lg" p="md" h="100%" className="widget-card" style={{ overflow: "hidden" }}>
    <Stack h="100%" gap="sm">
      <Group justify="space-between" align="center" wrap="nowrap" className="widget-drag" style={{ cursor: "grab" }}>
        {/* Title has no `truncate`; lineClamp is its own one-line ellipsis, and
            miw lets the flex item shrink so a long title never pushes the
            actions button out of the header. */}
        <Title order={2} fz="md" fw={500} lineClamp={1} miw={0}>{widget.title}</Title>
        <Menu position="bottom-end" withinPortal opened={menuOpened} onChange={setMenuOpened}>
          <Menu.Target>
            <ActionIcon className="widget-actions" data-open={menuOpened || undefined} variant="subtle" color="gray" size="sm" aria-label={`Actions for ${widget.title}`}><DotsThree size={18} weight="bold" /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            {configurable(widget.type) && <Menu.Item leftSection={<SlidersHorizontal size={15} />} onClick={() => setConfiguring(true)}>Configure</Menu.Item>}
            <Menu.Item color="bad" leftSection={<Trash size={15} />} onClick={() => setConfirmingRemove(true)}>Remove</Menu.Item>
          </Menu.Dropdown>
        </Menu>
      </Group>
      <div className="widget-body"><Body {...props} /></div>
    </Stack>
    <ConfigureWidget opened={configuring} widget={widget} services={services} onClose={() => setConfiguring(false)} onSave={onConfigure} />
    <Modal opened={confirmingRemove} onClose={() => setConfirmingRemove(false)} title={`Remove ${widget.title}?`} centered radius="md">
      <Stack gap="md">
        <Text size="sm" c="dimmed">This takes the card off the dashboard for everyone who opens it. You can add it again from Add view, but its size and settings are not kept.</Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="default" size="sm" onClick={() => setConfirmingRemove(false)}>Cancel</Button>
          <Button color="bad" size="sm" onClick={() => { setConfirmingRemove(false); onRemove(); }}>Remove</Button>
        </Group>
      </Stack>
    </Modal>
  </Paper>;
}
