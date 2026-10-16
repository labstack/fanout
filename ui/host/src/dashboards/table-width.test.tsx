import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { panelFragment } from "../../../panels/fragment";
import { formatTimestamp, formatValue } from "../../../panels/units";
import trace from "../../tests/go-fragments/trace.json";
import { RowPanel } from "./viz/row-panel";
import { TableViz } from "./viz/table";
it("fits the real trace projection to its card with ellipsis and complete time titles", async () => {
  const fragment = panelFragment(trace), node = document.createElement("div"), root = createRoot(node);
  try {
    await act(async () => root.render(<MantineProvider><RowPanel panel={fragment.dashboard.panels[0]} result={fragment.results[0]} dark={false} height={360} /></MantineProvider>));
    const table = node.querySelector<HTMLTableElement>("table")!;
    expect(table.style.tableLayout).toBe("auto"); expect(table.style.width).toBe("100%");
    const time = table.querySelector<HTMLElement>('td[data-field="start"] > *')!;
    expect(time.style.textOverflow).toBe("");
    expect(time.style.whiteSpace).toBe("nowrap");
    expect(time.style.overflow).toBe("");
    expect(time.parentElement?.hasAttribute("tabindex")).toBe(false);
    expect(table.querySelector<HTMLTableColElement>('col[data-field="start"]')?.style.width).toBe("146px");
    expect(table.querySelector<HTMLElement>('td[data-field="operation"] > *')?.style.textOverflow).toBe("ellipsis");
    expect(time.title).toBe("2026-10-07T19:15:01.000Z");
  } finally { await act(async () => root.unmount()); }
});

it.each(["light", "dark"] as const)("renders complete timestamps in a 12-column host table while clipping free-text cells (%s)", async theme => {
  const node = document.createElement("div"), root = createRoot(node);
  document.body.append(node);
  const timestamp = Date.parse("2026-10-16T12:20:02Z");
  const olderTimestamp = Date.parse("2025-10-16T12:20:02Z");
  const measurements = Array.from({ length: 10 }, (_, index) => ({ name: `measurement_${index}`, type: "string" as const, role: "dimension" as const }));
  try {
    await act(async () => root.render(<MantineProvider forceColorScheme={theme}>
      <TableViz panel={{ id: "hosts", title: "Hosts", viz: "table" }} height={200} onSelect={() => {}} result={{ id: "hosts", status: "ok", elapsed_ms: 1, frame: {
        columns: [{ name: "host", type: "string", role: "dimension" }, { name: "last_seen", type: "time", role: "time" }, ...measurements],
        values: [["a-very-long-host-name", "another-host"], [timestamp, olderTimestamp], ...measurements.map(() => ["measurement", "measurement"])], rows: 2,
      } }} />
    </MantineProvider>));
    for (const cell of node.querySelectorAll<HTMLElement>("tbody td")) {
      expect(cell.hasAttribute("tabindex")).toBe(false);
    }
    const time = node.querySelector<HTMLElement>('td[data-field="last_seen"] > *')!;
    expect(time.style.textOverflow).toBe("");
    expect(time.style.whiteSpace).toBe("nowrap");
    expect(time.style.overflow).toBe("");
    expect(time.title).toBe("2026-10-16T12:20:02.000Z");
    expect(time.textContent).toBe(formatTimestamp(timestamp));
    expect(node.querySelector<HTMLTableColElement>('col[data-field="last_seen"]')?.style.width).toBe("");
    expect(node.querySelector<HTMLElement>('td[data-field="measurement_0"] > *')?.classList.contains("dashboard-dimension-nowrap")).toBe(true);
    expect(node.querySelector<HTMLTableColElement>('col[data-field="measurement_0"]')?.style.width).toBe("");
    const row = node.querySelector<HTMLTableRowElement>("tbody tr")!;
    expect(row.tabIndex).toBe(0);
    await act(async () => row.focus());
    expect(document.activeElement).toBe(row);
    expect(node.querySelector('td[data-field="measurement_0"]')?.textContent).toBe("measurement");
  } finally { await act(async () => root.unmount()); node.remove(); }
});


it.each(["light", "dark"] as const)("renders complete status badges and numeric labels alongside free text (%s)", async theme => {
  const node = document.createElement("div"), root = createRoot(node);
  node.style.width = "600px";
  const host = "a-host-name-that-should-ellipsize";
  const json = '{"description":"a long JSON value"}';
  try {
    await act(async () => root.render(<MantineProvider forceColorScheme={theme}>
      <TableViz panel={{ id: "hosts", title: "Hosts", viz: "table", options: { columns: [
        { field: "status", format: "status" }, { field: "state", format: "status" },
        { field: "rate", format: "status" }, { field: "latency", format: "unit" }, { field: "usage", format: "bar" },
      ] } }} height={200} result={{ id: "hosts", status: "ok", elapsed_ms: 1, frame: {
        columns: [
          { name: "host", type: "string", role: "dimension" },
          { name: "status", type: "number", role: "measure" },
          { name: "count", type: "number", role: "measure" },
          { name: "state", type: "string", role: "dimension" },
          { name: "rate", type: "string", role: "measure", unit: "percent" },
          { name: "latency", type: "number", role: "measure", unit: "ms" },
          { name: "usage", type: "number", role: "measure", unit: "bytes" },
          { name: "attributes", type: "json", role: "dimension" },
        ],
        values: [[host, "short"], [53.41, 1], [1234567.89, -123456789.12], ["healthy", "unhealthy"], ["1.2746", "0"], [900, 1200], [1024, 2048], [json, "{}"]], rows: 2,
      } }} />
    </MantineProvider>));
    const badge = node.querySelector<HTMLElement>('td[data-field="status"] > *')!;
    expect(badge.textContent).toBe("○ 53.41");
    expect(node.querySelector('td[data-field="rate"]')?.textContent).toBe("○ 1.27%");
    expect(node.querySelector('td[data-field="count"]')?.textContent).toBe(formatValue(undefined, 1234567.89));
    for (const [field, value] of [["host", host], ["attributes", json]]) {
      const cell = node.querySelector<HTMLElement>(`td[data-field="${field}"]`)!;
      expect(cell.firstElementChild?.getAttribute("title")).toBe(value);
      expect(cell.firstElementChild?.classList.contains("dashboard-dimension-nowrap")).toBe(true);
      expect(node.querySelector<HTMLTableColElement>(`col[data-field="${field}"]`)?.style.width).toBe("");
    }
  } finally { await act(async () => root.unmount()); }
});

it("renders each custom cell once without probing it to estimate widths", async () => {
  const node = document.createElement("div"), root = createRoot(node);
  const Content = vi.fn(() => <span>custom value</span>);
  const renderCell = vi.fn(() => <Content />);
  try {
    await act(async () => root.render(<MantineProvider>
      <TableViz renderCell={renderCell} panel={{ id: "hosts", title: "Hosts", viz: "table" }} height={200} result={{ id: "hosts", status: "ok", elapsed_ms: 1, frame: {
        columns: [{ name: "host", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure" }],
        values: [["host"], [12345]], rows: 1,
      } }} />
    </MantineProvider>));
    expect(renderCell.mock.calls.length).toBeGreaterThan(0);
    expect(renderCell.mock.calls.length).toBe(Content.mock.calls.length);
    expect(node.querySelector('td[data-field="count"]')?.textContent).toBe("custom value");
  } finally { await act(async () => root.unmount()); }
});
