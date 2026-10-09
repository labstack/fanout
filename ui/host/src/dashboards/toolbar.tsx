import { Button, Group, Menu, SegmentedControl, Switch, Text, TextInput, Tooltip } from "@mantine/core";
import { ArrowClockwise, CaretDown, Clock, ClockCounterClockwise, MagnifyingGlassMinus, PencilSimple } from "@phosphor-icons/react";
import { useState } from "react";
import type { DashboardTime } from "../../../panels/types";
import { exactTimestamp } from "../../../format";
import { ShortcutButton } from "./shortcut-button";
import { ranges, refreshes } from "./search";

const rangeLabel: Record<string, string> = { "5m": "Last 5 minutes", "15m": "Last 15 minutes", "1h": "Last hour", "3h": "Last 3 hours", "6h": "Last 6 hours", "12h": "Last 12 hours", "24h": "Last 24 hours", "2d": "Last 2 days", "7d": "Last 7 days", "30d": "Last 30 days" };

function timeLabel(time: DashboardTime): string {
  if (time.from && time.to) return `${exactTimestamp(time.from)} – ${exactTimestamp(time.to)}`;
  return rangeLabel[time.range ?? "1h"] ?? time.range ?? "Last hour";
}

export function Toolbar({ time, refresh, compare, editing, fetching, updatedAt, onRange, onAbsolute, onZoomOut, onRefresh, onRefreshNow, onCompare, onEdit, onHistory, onShortcuts, selecting = false }: {
  time: DashboardTime; refresh: string; compare: boolean; editing: boolean; fetching: boolean; updatedAt: number | null;
  onRange(range: string): void; onAbsolute(from: string, to: string): void; onZoomOut(): void; onRefresh(refresh: string): void; onRefreshNow(): void; onCompare(on: boolean): void; onEdit(): void; onHistory(): void;
  onShortcuts?(): void; selecting?: boolean;
}) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [opened, setOpened] = useState(false);
  return <Group gap="xs" wrap="wrap" role="group" aria-label="Dashboard controls">
    <Menu position="bottom-end" withinPortal opened={opened} onChange={setOpened}>
      <Menu.Target>
        <Button variant="default" size="sm" leftSection={<Clock size={15} />} rightSection={<CaretDown size={12} weight="bold" />}>{timeLabel(time)}</Button>
      </Menu.Target>
      <Menu.Dropdown miw={260}>
        <Menu.Label>Relative</Menu.Label>
        {ranges.map((range) => <Menu.Item key={range} onClick={() => onRange(range)} fw={time.range === range ? 600 : undefined}>{rangeLabel[range]}</Menu.Item>)}
        <Menu.Divider />
        <Menu.Label>Absolute (your time zone)</Menu.Label>
        <Group gap={6} px="xs" pb="xs" wrap="nowrap">
          <TextInput type="datetime-local" size="xs" aria-label="From" value={from} onChange={(e) => setFrom(e.currentTarget.value)} />
          <TextInput type="datetime-local" size="xs" aria-label="To" value={to} onChange={(e) => setTo(e.currentTarget.value)} />
        </Group>
        <Group px="xs" pb="xs" justify="flex-end">
          <Button size="compact-sm" disabled={!from || !to || from >= to} onClick={() => { onAbsolute(new Date(from).toISOString(), new Date(to).toISOString()); setOpened(false); }}>Apply range</Button>
        </Group>
      </Menu.Dropdown>
    </Menu>
    <Tooltip label="Zoom out"><Button variant="default" size="sm" px={10} aria-label="Zoom out" onClick={onZoomOut}><MagnifyingGlassMinus size={15} /></Button></Tooltip>
    <Group data-refresh-controls gap={4} wrap="nowrap">
      <Tooltip label={updatedAt ? `Updated ${exactTimestamp(updatedAt)}` : "Refresh now"}><Button variant="default" size="sm" px={10} aria-label="Refresh now" loading={fetching} onClick={onRefreshNow}><ArrowClockwise size={15} /></Button></Tooltip>
      <SegmentedControl size="xs" aria-label="Auto refresh" value={refresh} onChange={onRefresh} data={refreshes.map((r) => ({ value: r, label: r === "off" ? "Off" : r }))} />
    </Group>
    <Switch size="sm" label="Compare with previous period" checked={compare} onChange={(e) => onCompare(e.currentTarget.checked)} />
    <Button variant={editing ? "filled" : "default"} size="sm" leftSection={<PencilSimple size={15} />} onClick={onEdit}>{editing ? "Done" : "Edit layout"}</Button>
    <Button variant="default" size="sm" leftSection={<ClockCounterClockwise size={15} />} onClick={onHistory}>History</Button>
    {onShortcuts && <ShortcutButton onClick={onShortcuts}/>}
    {updatedAt && <Text size="xs" c="dimmed" visibleFrom="md">Updated {new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit", second: "2-digit" }).format(updatedAt)}</Text>}
    {selecting && <Text data-range-paused size="xs" c="dimmed" role="status">Paused while selecting</Text>}
  </Group>;
}
