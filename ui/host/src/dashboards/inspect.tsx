import { Code, Table, Text } from "@mantine/core";
import type { Panel, PanelResult } from "../../../panels/types";
import { formatValue } from "../../../panels/units";
import { brand, info, ok, chart, fonts } from "../../../tokens";

export function PanelData({ panel, result }: { panel: Panel; result?: PanelResult }) {
  const frame = result?.frame;
  return <div data-panel-data>
    {result?.error && <Text data-panel-error-detail size="sm" role="status"
      style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere",userSelect:"text"}}>
      {result.error}
    </Text>}
    {frame ? <Table fz={12} striped>
    <Table.Thead><Table.Tr>{frame.columns.map(c => <Table.Th key={c.name} ff={fonts.display}>{c.name}</Table.Th>)}</Table.Tr></Table.Thead>
    <Table.Tbody>{Array.from({ length: Math.min(frame.rows, 500) }, (_, r) => <Table.Tr key={r}>{frame.columns.map((c, i) => {
      const v = frame.values[i][r];
      return <Table.Td key={c.name} ff={fonts.display}>{v === null ? "—" : c.type === "time" ? new Date(Number(v)).toISOString() : c.role === "measure" ? formatValue(c.unit ?? panel.unit, Number(v)) : String(v)}</Table.Td>;
    })}</Table.Tr>)}</Table.Tbody>
  </Table> : <Text c="dimmed" size="sm">No data.</Text>}
    <Text c="dimmed" fz={12} mt={8}>Ran in {result?.elapsed_ms ?? 0} ms{result?.interval ? `, one point per ${result.interval}` : ""}{frame ? `, ${frame.rows} rows${frame.truncated ? " (truncated)" : ""}` : ""}.</Text>
    {(result?.sql ?? panel.sql) && <Code block tabIndex={0} mt={8}>{result?.sql ?? panel.sql}</Code>}
  </div>;
}

export function PanelSpec({ panel, dark }: { panel: Panel; dark: boolean }) {
  const json = JSON.stringify(panel, null, 2);
  const parts = json.split(/("(?:[^"\\]|\\.)*"(?=\s*:)|"(?:[^"\\]|\\.)*"|\b(?:true|false|null)\b|-?\d+(?:\.\d+)?)/g);
  return <pre data-panel-spec style={{ margin: 0, fontFamily: fonts.display, fontSize: 12, color: chart[dark ? "dark" : "light"].text }}>{parts.map((part, i) => {
    const key = part.startsWith('"') && /^\s*:/.test(parts[i + 1] ?? "");
    const kind = key ? "key" : part.startsWith('"') ? "string" : /^(true|false|null)$/.test(part) ? "literal" : /^-?\d/.test(part) ? "number" : "punctuation";
    const color = kind === "key" ? brand[dark ? 4 : 7] : kind === "string" ? ok[dark ? 4 : 8] : kind === "number" || kind === "literal" ? info[dark ? 4 : 8] : undefined;
    // Punctuation takes the block's own colour, so it stays plain text: a span
    // around a multi-line run of braces leaves contrast checks no line box to measure.
    if (kind === "punctuation") return part;
    return <span key={i} data-json-token={kind} style={{ color }}>{part}</span>;
  })}</pre>;
}
