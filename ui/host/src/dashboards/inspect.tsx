import { DataState, Notice } from "@labstack/ui";
import { Code, Table, Text } from "@mantine/core";
import type { Panel, PanelResult } from "../../../panels/types";
import { formatValue } from "../../../panels/units";
import { info, ok, fonts } from "../../../tokens";
import { duration, plural } from "../../../format";
import { chart } from "../../../theme";

export function PanelData({ panel, result }: { panel: Panel; result?: PanelResult }) {
  const frame = result?.frame;
  return <div data-panel-data>
    {result?.error && <Notice tone="danger"><Text data-panel-error-detail size="sm" style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere",userSelect:"text"}}>{result.error}</Text></Notice>}
    {result?.problems?.map((problem,i) => <Text key={i} size="sm"><code>{problem.path}</code>{problem.message !== result?.error && `: ${problem.message}`}{problem.hint && ` — ${problem.hint}`}</Text>)}
    {frame ? <Table fz={12} stickyHeader highlightOnHover>
    <Table.Thead><Table.Tr>{frame.columns.map(c => <Table.Th ta={c.type === "number" ? "right" : undefined} key={c.name} ff={fonts.display}>{c.name}</Table.Th>)}</Table.Tr></Table.Thead>
    <Table.Tbody>{Array.from({ length: Math.min(frame.rows, 500) }, (_, r) => <Table.Tr key={r}>{frame.columns.map((c, i) => {
      const v = frame.values[i][r];
      return <Table.Td ta={c.type === "number" ? "right" : undefined} key={c.name} ff={fonts.display}>{v === null ? "—" : c.type === "time" ? new Date(Number(v)).toISOString() : c.role === "measure" ? formatValue(c.unit ?? panel.unit, Number(v)) : String(v)}</Table.Td>;
    })}</Table.Tr>)}</Table.Tbody>
  </Table> : <DataState state="empty" empty={{ title: "No data." }} />}
    {result && result.status !== "invalid" && result.elapsed_ms !== undefined && <Text c="dimmed" fz={12} mt={8}>Ran in {duration(result.elapsed_ms)}{result?.interval ? `, one point per ${result.interval}` : ""}{frame ? `, ${plural(frame.rows, "row")}${frame.truncated ? " (truncated)" : ""}` : ""}.</Text>}
    {(result?.sql ?? panel.sql) && <Code block tabIndex={0} mt={8}>{result?.sql ?? panel.sql}</Code>}
  </div>;
}

export function PanelSpec({ panel, dark }: { panel: Panel; dark: boolean }) {
  const json = JSON.stringify(panel, null, 2);
  const parts = json.split(/("(?:[^"\\]|\\.)*"(?=\s*:)|"(?:[^"\\]|\\.)*"|\b(?:true|false|null)\b|-?\d+(?:\.\d+)?)/g);
  return <pre data-panel-spec style={{ margin: 0, fontFamily: fonts.display, fontSize: 12, color: chart[dark ? "dark" : "light"].text }}>{parts.map((part, i) => {
    const key = part.startsWith('"') && /^\s*:/.test(parts[i + 1] ?? "");
    const kind = key ? "key" : part.startsWith('"') ? "string" : /^(true|false|null)$/.test(part) ? "literal" : /^-?\d/.test(part) ? "number" : "punctuation";
    const color = kind === "key" ? chart[dark ? "dark" : "light"].text : kind === "string" ? ok[dark ? 4 : 8] : kind === "number" || kind === "literal" ? info[dark ? 4 : 8] : undefined;
    // Punctuation takes the block's own colour, so it stays plain text: a span
    // around a multi-line run of braces leaves contrast checks no line box to measure.
    if (kind === "punctuation") return part;
    return <span key={i} data-json-token={kind} style={{ color }}>{part}</span>;
  })}</pre>;
}
