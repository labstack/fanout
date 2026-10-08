import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { brushRange, panelTimeLabel, pointSelection } from "../../../panels/interaction";
import { parseSearch, toSearchParams, type DashboardSearch } from "./search";
import type { Panel, PanelResult } from "../../../panels/types";
import { useBrushZoom } from "./use-brush-zoom";

const panel: Panel = { id: "p", title: "P", viz: "timeseries", query: { from: "spans", measures: ["count()"], by: ["service"] }, time: { shift: "1d" } };
const result: PanelResult = { id: "p", status: "ok", elapsed_ms: 1, from_ms: 1000, to_ms: 10000, interval: "1m" };
describe("M2 interactions", () => {
  it("writes one valid absolute range, retaining comparison and variables", () => {
    const selected = brushRange(1000, 9000); expect(selected).toEqual({ from: new Date(1000).toISOString(), to: new Date(9000).toISOString() });
    const state = { ...parseSearch({ range: "1h", "var-service": "checkout", compare: "1" }), ...selected, range: undefined };
    expect(parseSearch(toSearchParams(state))).toEqual({ from: selected?.from, to: selected?.to, vars: { service: "checkout" }, compare: "1" });
    expect(brushRange(2, 2)).toBeUndefined(); expect(brushRange(NaN, 3)).toBeUndefined();
  });
  it("selects real dimensions and never Other or comparison overlays", () => {
    expect(pointSelection(panel, result, { seriesName: "checkout", value: [2000, 3] })).toEqual({ time: 2000, dimensions: { service: "checkout" } });
    expect(pointSelection(panel, result, { seriesName: "Other", value: [2000, 3] })).toBeUndefined();
    expect(pointSelection(panel, result, { seriesName: "checkout · previous", value: [2000, 3] })).toBeUndefined();
    expect(panelTimeLabel(panel)).toBe("Shifted 1d");
  });

  it("decodes SQL frame dimensions without selecting ungrouped measure names", () => {
    const sqlPanel: Panel = { id: "p", title: "SQL", viz: "timeseries", sql: "SELECT 1" };
    const grouped: PanelResult = { ...result, frame: { columns: [{ name: "service", type: "string", role: "dimension" }], values: [["checkout"]], rows: 1 } };
    expect(pointSelection(sqlPanel, grouped, { seriesName: "checkout", value: [2000, 3] })).toEqual({ time: 2000, dimensions: { service: "checkout" } });
    expect(pointSelection(sqlPanel, result, { seriesName: "count", value: [2000, 3] })?.dimensions).toEqual({});
  });

  it("rounds fractional endpoints outward and rejects invalid date ranges", () => {
    expect(brushRange(1000.5, 9000.1)).toEqual({ from: new Date(1000).toISOString(), to: new Date(9001).toISOString() });
    for (const [from, to] of [[3, 2], [1, Infinity], [-8640000000000001, 0], [0, 8640000000000001]]) expect(brushRange(from, to)).toBeUndefined();
    expect(panelTimeLabel({ ...panel, time: { range: "15m", shift: "1h" } })).toBe("Range 15m · Shifted 1h");
    expect(panelTimeLabel({ ...panel, time: undefined })).toBeUndefined();
  });
});

it("pushes once per selection, preserves variables, and permits the same selection after Back", async () => {
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node); const navigate = vi.fn();
  let actions: ReturnType<typeof useBrushZoom>;
  function Host({ search }: { search: DashboardSearch }) { actions = useBrushZoom(search, navigate); return null; }
  const initial: DashboardSearch = { range: "1h", vars: { service: "checkout" }, compare: "1" };
  await act(async () => root.render(<Host search={initial} />));
  for (let i = 0; i < 20; i++) actions!.zoom(1000, 9000);
  expect(navigate).toHaveBeenCalledTimes(1); expect(navigate.mock.calls[0][1]).toBe(false); expect(navigate.mock.calls[0][0].vars).toEqual(initial.vars);
  const brushed = navigate.mock.calls[0][0] as DashboardSearch;
  await act(async () => root.render(<Host search={brushed} />)); actions!.zoom(1000, 9000); expect(navigate).toHaveBeenCalledTimes(1);
  await act(async () => root.render(<Host search={initial} />)); actions!.zoom(1000, 9000); expect(navigate).toHaveBeenCalledTimes(2);
  await act(async () => root.unmount()); node.remove();
});
