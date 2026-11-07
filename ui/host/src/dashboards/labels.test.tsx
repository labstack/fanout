import { MantineProvider } from "@mantine/core";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, it } from "vitest";
import { displayLabel } from "../../../panels/labels";
import { analysisOption, analysisSummary } from "../../../panels/analysis";
import { chartThemeFor, timeseriesOption, barOption } from "../../../panels/compile";
import type { Column, Panel, PanelResult } from "../../../panels/types";
import { TableViz } from "./viz/table";

it.each([
  ["avg_ms", "Avg (ms)"], ["p95_duration_ms", "p95 duration (ms)"],
  ["error_rate", "Error rate"], ["body_template", "Message template"],
  ["http_status_code", "HTTP status code"], ["trace_id", "Trace ID"],
  ["cpu_time_ns", "CPU time (ns)"], ["CAST(time AS TIMESTAMPTZ_NS)", "Value"], ["p99(duration_ms)", "p99 duration (ms)"],
  ["service_rollup", "Service rollup"], ["edge_rollup", "Edge rollup"], ["resource_inventory", "Resource inventory"],
])("displays %s as %s", (key, label) => expect(displayLabel(key)).toBe(label));

it("honours explicit units before suffix inference", () => {
  const column: Column = {name:"avg_ms",type:"number",role:"measure",unit:"s"};
  expect(displayLabel(column)).toBe("Avg (s)");
  expect(displayLabel(column,{id:"p",title:"P",viz:"table",options:{columns:[{field:"avg_ms",format:"unit",unit:"ns"}]}})).toBe("Avg (ns)");
});

it("uses shared labels for tables, legends, axes, tooltips and accessible descriptions while retaining keys", () => {
  const column: Column = {name:"p95_duration_ms",type:"number",role:"measure",unit:"ms"};
  const panel: Panel = {id:"p",title:"Latency",viz:"table"};
  const result: PanelResult = {id:"p",status:"ok",elapsed_ms:1,frame:{columns:[{name:"time",type:"time",role:"time"},column],values:[[0],[42]],rows:1}};
  const original = JSON.stringify(result);
  const table = renderToStaticMarkup(<MantineProvider><TableViz panel={panel} result={result} height={240}/></MantineProvider>);
  // Table cells print durations in their own unit, so the header names none.
  expect(table).toContain(">p95 duration<");
  expect(table).not.toContain("p95 duration (ms)");
  expect(table).toContain('data-field="p95_duration_ms"');
  const theme = chartThemeFor(false);
  const line = timeseriesOption({...panel,viz:"timeseries"},result,theme) as any;
  expect(line.series[0].name).toBe("p95 duration (ms)");
  expect(line.tooltip.formatter([{seriesName:line.series[0].name,value:[0,42]}])).toContain("p95 duration (ms)");
  const scatterResult: PanelResult = {...result,frame:{columns:[{name:"service",type:"string",role:"dimension"},{name:"avg_ms",type:"number",role:"measure",unit:"ms"},column],values:[["checkout_service"],[12],[42]],rows:1}};
  const scatter = analysisOption({...panel,viz:"scatter"},scatterResult,theme) as any;
  expect(scatter.xAxis.name).toBe("Avg (ms)"); expect(scatter.yAxis.name).toBe("p95 duration (ms)");
  expect(scatter.tooltip.formatter({seriesName:"Items",name:"checkout_service",value:[12,42]})).toContain("Avg (ms)");
  expect(analysisSummary(panel,scatterResult)).toContain("p95 duration (ms)");
  const bar = barOption({...panel,viz:"bar"},scatterResult.frame!,theme) as any;
  expect(bar.series.map((s:any)=>s.name)).toEqual(["Avg (ms)","p95 duration (ms)"]);
  expect(bar.yAxis.data).toEqual(["checkout_service"]);
  expect(JSON.stringify(result)).toBe(original);
});

it("heads a table duration column without the unit its cells already print", () => {
  // A max_ms column of 600014 ms printed "10m" under "Max (ms)".
  const panel: Panel = {id:"p",title:"Slowest",viz:"table",unit:"ms"};
  const result: PanelResult = {id:"p",status:"ok",elapsed_ms:1,frame:{columns:[{name:"service",type:"string",role:"dimension"},{name:"max_ms",type:"number",role:"measure"},{name:"calls",type:"number",role:"measure",unit:"count"}],values:[["checkout"],[600014],[3]],rows:1}};
  const table = renderToStaticMarkup(<MantineProvider><TableViz panel={panel} result={result} height={240}/></MantineProvider>);
  expect(table).toContain(">Max<");
  expect(table).not.toContain("Max (ms)");
  expect(table).toContain(">10m<");
  // Without a duration unit the cell is a bare number and the header keeps the name's unit.
  const bare = renderToStaticMarkup(<MantineProvider><TableViz panel={{...panel,unit:undefined}} result={result} height={240}/></MantineProvider>);
  expect(bare).toContain("Max (ms)");
});

it("keeps the unit in a duration column's header when its format prints raw values", () => {
  const result: PanelResult = {id:"p",status:"ok",elapsed_ms:1,frame:{columns:[{name:"max_ms",type:"number",role:"measure",unit:"ms"}],values:[[600014]],rows:1}};
  const header = (format: "service_link" | "trace_link" | "bar" | "status") => renderToStaticMarkup(<MantineProvider><TableViz panel={{id:"p",title:"Slowest",viz:"table",options:{columns:[{field:"max_ms",format}]}}} result={result} height={240}/></MantineProvider>);
  // These print the cell as it came, so the header has to say what it is in.
  expect(header("service_link")).toContain("Max (ms)");
  expect(header("trace_link")).toContain("Max (ms)");
  // These print durations in their own unit.
  expect(header("bar")).not.toContain("Max (ms)");
  expect(header("status")).not.toContain("Max (ms)");
});
