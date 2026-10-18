import { MantineProvider } from "@mantine/core";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, it } from "vitest";
import { displayLabel } from "../../../panels/labels";
import { analysisOption, analysisSummary } from "../../../panels/analysis";
import { chartThemeFor, timeseriesOption, barOption } from "../../../panels/compile";
import type { Column, Panel, PanelResult } from "../../../panels/types";
import { TableViz } from "./viz/table";

it.each([
  ["avg_ms", "Avg (ms)"], ["p95_duration_ms", "P95 duration (ms)"],
  ["error_rate", "Error rate"], ["body_template", "Message template"],
  ["http_status_code", "HTTP status code"], ["trace_id", "Trace ID"],
  ["cpu_time_ns", "CPU time (ns)"], ["CAST(time AS TIMESTAMPTZ_NS)", "Value"], ["p99(duration_ms)", "P99 duration (ms)"],
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
  expect(table).toContain("P95 duration (ms)");
  expect(table).toContain('data-field="p95_duration_ms"');
  const theme = chartThemeFor(false);
  const line = timeseriesOption({...panel,viz:"timeseries"},result,theme) as any;
  expect(line.series[0].name).toBe("P95 duration (ms)");
  expect(line.tooltip.formatter([{seriesName:line.series[0].name,value:[0,42]}])).toContain("P95 duration (ms)");
  const scatterResult: PanelResult = {...result,frame:{columns:[{name:"service",type:"string",role:"dimension"},{name:"avg_ms",type:"number",role:"measure",unit:"ms"},column],values:[["checkout_service"],[12],[42]],rows:1}};
  const scatter = analysisOption({...panel,viz:"scatter"},scatterResult,theme) as any;
  expect(scatter.xAxis.name).toBe("Avg (ms)"); expect(scatter.yAxis.name).toBe("P95 duration (ms)");
  expect(scatter.tooltip.formatter({seriesName:"Items",name:"checkout_service",value:[12,42]})).toContain("Avg (ms)");
  expect(analysisSummary(panel,scatterResult)).toContain("P95 duration (ms)");
  const bar = barOption({...panel,viz:"bar"},scatterResult.frame!,theme) as any;
  expect(bar.series.map((s:any)=>s.name)).toEqual(["Avg (ms)","P95 duration (ms)"]);
  expect(bar.yAxis.data).toEqual(["checkout_service"]);
  expect(JSON.stringify(result)).toBe(original);
});
