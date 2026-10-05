import { Code, Drawer, ScrollArea, Table, Tabs, Text } from "@mantine/core";
import type { Panel, PanelResult } from "../../../panels/types";
import { formatValue } from "../../../panels/units";

export function InspectDrawer({ panel, result, onClose }: { panel?: Panel; result?: PanelResult; onClose(): void }) {
  const frame = result?.frame;
  return <Drawer opened={Boolean(panel)} onClose={onClose} position="right" size="xl" closeButtonProps={{ "aria-label": "Close inspect" }} title={panel ? `Inspect · ${panel.title}` : ""}>
    {panel && <Tabs defaultValue="data">
      <Tabs.List>
        <Tabs.Tab value="data">Data</Tabs.Tab>
        <Tabs.Tab value="query">Query</Tabs.Tab>
        <Tabs.Tab value="spec">Spec</Tabs.Tab>
        <Tabs.Tab value="timing">Timing</Tabs.Tab>
      </Tabs.List>
      <Tabs.Panel value="data" pt="sm">
        {frame ? <ScrollArea h="75vh"><Table fz="xs" striped>
          <Table.Thead><Table.Tr>{frame.columns.map((c) => <Table.Th key={c.name} ff="monospace">{c.name}</Table.Th>)}</Table.Tr></Table.Thead>
          <Table.Tbody>{Array.from({ length: Math.min(frame.rows, 500) }, (_, r) => <Table.Tr key={r}>{frame.columns.map((c, i) => {
            const v = frame.values[i][r];
            return <Table.Td key={c.name} ff="monospace">{v === null ? "—" : c.type === "time" ? new Date(Number(v)).toISOString() : c.role === "measure" ? formatValue(c.unit ?? panel.unit, Number(v)) : String(v)}</Table.Td>;
          })}</Table.Tr>)}</Table.Tbody>
        </Table></ScrollArea> : <Text c="dimmed" size="sm">No data.</Text>}
      </Tabs.Panel>
      <Tabs.Panel value="query" pt="sm"><Code block>{result?.sql ?? panel.sql ?? "—"}</Code></Tabs.Panel>
      <Tabs.Panel value="spec" pt="sm"><Code block>{JSON.stringify(panel, null, 2)}</Code></Tabs.Panel>
      <Tabs.Panel value="timing" pt="sm">
        <Text size="sm">Ran in {result?.elapsed_ms ?? 0} ms{result?.interval ? `, one point per ${result.interval}` : ""}{frame ? `, ${frame.rows} rows${frame.truncated ? " (truncated)" : ""}` : ""}.</Text>
      </Tabs.Panel>
    </Tabs>}
  </Drawer>;
}
