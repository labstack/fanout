import { ActionIcon, Group, Menu, Paper, Stack, Title } from "@mantine/core";
import { DotsThree, SlidersHorizontal, Trash } from "@phosphor-icons/react";
import { useState, type JSX } from "react";
import type { WidgetType } from "../dashboard-layout";
import ActivityWidget from "./activity";
import AssistantWidget from "./assistant";
import { ConfigureWidget, configurable } from "./configure";
import type { Filters, WidgetConfig } from "./data";
import LogsWidget from "./logs";
import OverviewWidget from "./overview";
import PerformanceWidget from "./performance";
import TopologyWidget from "./topology";
import TraceWidget from "./trace";

export type Widget = { id: string; type: WidgetType; title: string; config?: WidgetConfig; enabled: boolean };

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

export default function WidgetCard(props: WidgetBodyProps & { onRemove: () => void; onConfigure: (config: WidgetConfig) => void }) {
  const { widget, services, onRemove, onConfigure } = props;
  const [menuOpened, setMenuOpened] = useState(false);
  const [configuring, setConfiguring] = useState(false);
  const Body = bodies[widget.type];
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
            <Menu.Item color="bad" leftSection={<Trash size={15} />} onClick={onRemove}>Remove</Menu.Item>
          </Menu.Dropdown>
        </Menu>
      </Group>
      <div className="widget-body"><Body {...props} /></div>
    </Stack>
    <ConfigureWidget opened={configuring} widget={widget} services={services} onClose={() => setConfiguring(false)} onSave={onConfigure} />
  </Paper>;
}
