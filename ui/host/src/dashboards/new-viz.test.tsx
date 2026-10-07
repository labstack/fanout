import { init, use } from "echarts/core";
import { BarChart, CustomChart, GraphChart, ScatterChart } from "echarts/charts";
import { GridComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import { MantineProvider } from "@mantine/core";
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { analysisOption, analysisSummary } from "../../../panels/analysis";
import { chartThemeFor, timeseriesOption } from "../../../panels/compile";
import {visualizations,type Panel,type PanelResult,type Viz as VizType} from "../../../panels/types";
import { PanelCard } from "./panel-card";
import { InspectDrawer } from "./inspect";
import { frameRows, rowModel } from "../../../panels/rows";
import { healthSymbol } from "../../../chart";
import { TableViz } from "./viz/table";
import { Viz } from "./viz";
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
vi.mock("./echart-canvas",()=>({EChartCanvas:({label}:{label:string})=><div role="img" aria-label={label}/> }));
const types:VizType[]=["heatmap","histogram","scatter","state_timeline","logs","log_patterns","traces","service_map","health"];
const rowTypes = new Set<VizType>(["logs", "log_patterns", "traces"]);
const col=(name:string,type:"time"|"number"|"string"|"json",role:"time"|"dimension"|"measure",unit?:string)=>({name,type,role,unit});
const fixtures:Record<string,PanelResult["frame"]>={
 heatmap:{columns:[col("time","time","time"),col("bucket_lower","number","dimension","ms"),col("bucket_upper","number","dimension","ms"),col("count","number","measure","count")],values:[[1000],[1],[2],[3]],rows:1},
 histogram:{columns:[col("bucket_lower","number","dimension","ms"),col("bucket_upper","number","dimension","ms"),col("count","number","measure","count")],values:[[1],[2],[3]],rows:1},
 scatter:{columns:[col("service","string","dimension"),col("count","number","measure","count"),col("p95","number","measure","ms")],values:[["checkout"],[2],[50]],rows:1},
 state_timeline:{columns:[col("time","time","time"),col("service","string","dimension"),col("p95","number","measure","ms")],values:[[1000],["checkout"],[50]],rows:1},
 logs:{columns:[col("time","time","time"),col("severity","string","dimension"),col("service","string","dimension"),col("body","string","dimension"),col("trace_id","string","dimension"),col("namespace","string","dimension")],values:[[1000],["ERROR"],["checkout"],["failed"],["abc"],["shop"]],rows:1},
 log_patterns:{columns:[col("body_template","string","dimension"),col("count","number","measure","count"),col("trend","json","dimension","count")],values:[["failed <*>"],[2],["[1,1]"]],rows:1},
 traces:{columns:[col("trace_id","string","dimension"),col("namespace","string","dimension"),col("service","string","dimension"),col("operation","string","dimension"),col("duration_ms","number","measure","ms"),col("status","string","dimension"),col("start","time","time")],values:[["abc"],["shop"],["checkout"],["cart"],[50],["STATUS_CODE_ERROR"],[1000]],rows:1},
 service_map:{columns:[col("kind","string","dimension"),col("service","string","dimension"),col("caller","string","dimension"),col("callee","string","dimension"),col("edge_type","string","dimension"),col("calls","number","measure","count"),col("average_ms","number","measure","ms"),col("error_rate","number","measure","percent"),col("health","string","dimension"),col("p95_ms","number","measure","ms"),col("spans","number","measure","count")],values:[["node","edge"],["checkout",""],["","checkout"],["","payment"],["","call"],[null,3],[null,20],[10,1],["unhealthy",""],[50,null],[2,null]],rows:2},
 health:{columns:[col("service","string","dimension"),col("health","string","dimension"),col("spans","number","measure","count"),col("error_rate","number","measure","percent"),col("p50_ms","number","measure","ms"),col("p95_ms","number","measure","ms"),col("log_count","number","measure","count"),col("metric_count","number","measure","count")],values:[["checkout"],["unhealthy"],[2],[10],[20],[50],[1],[1]],rows:1,health:{health:"unhealthy",counts:{healthy:0,degraded:0,unhealthy:1},service_count:1,total_spans:2,error_rate:10,error_trend:[1,10]}},
};
const resultFor=(viz:string):PanelResult=>({id:"p",status:"ok",elapsed_ms:1,interval:"1m",from_ms:0,to_ms:10000,frame:fixtures[viz]});
const assertFinite=(value:unknown):void=>{if(typeof value==="number")expect(Number.isFinite(value)).toBe(true);else if(Array.isArray(value))value.forEach(assertFinite);else if(value&&typeof value==="object")Object.values(value).forEach(assertFinite);};

describe("M2 visualizations",()=>{
  it("has exactly fifteen registry entries",()=>{
    expect(visualizations).toHaveLength(15);expect(new Set(visualizations).size).toBe(15);
  });
  it("compiles empty chart frames in both themes without nonfinite values",()=>{
    for(const dark of [false,true])for(const viz of types){
      const panel:Panel={id:"p",title:viz,viz,thresholds:[{value:1,status:"bad"}]};
      const result=resultFor(viz);const empty={...result,frame:{...result.frame!,values:result.frame!.columns.map(()=>[]),rows:0,health:undefined}} as PanelResult;
      assertFinite(analysisOption(panel,empty,chartThemeFor(dark)));
      const nonfinite={...result,frame:{...result.frame!,values:result.frame!.values.map(values=>values.map(v=>typeof v==="number"?Infinity:v))}};assertFinite(analysisOption(panel,nonfinite,chartThemeFor(dark)));
      expect(analysisSummary(panel,empty)).toContain("0");
    }
  });
  it("renders loading, empty, error and partial states for all types",async()=>{
    const container=document.createElement("div");document.body.append(container);const root=createRoot(container);
    const noop=()=>undefined;
    for(const viz of types){
      const result=resultFor(viz);
      const panel:Panel={id:"p",title:viz,viz};
      const props={panel,title:viz,height:300,group:"g",editing:false,agentAvailable:false,onView:noop,onInspect:noop,onCopyLink:noop,onExplain:noop};
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading result={undefined}/></MantineProvider>));
      expect(container.querySelector('[aria-label="Loading panel"]')).not.toBeNull();
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={{...result,status:"empty",diagnosis:"No matching events"}}/></MantineProvider>));
      expect(container.textContent).toContain("No matching events");
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={{...result,status:"error",error:"Query failed"}}/></MantineProvider>));
      expect(container.textContent).toContain("Query failed");
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={{...result,frame:{...result.frame!,truncated:true}}}/></MantineProvider>));
      expect(container.textContent).toContain("Truncated");
      const zero={...result,frame:{...result.frame!,values:result.frame!.columns.map(()=>[]),rows:0,health:undefined}} as PanelResult;
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={zero}/></MantineProvider>));
      expect(container.textContent).not.toMatch(/NaN|Infinity/);
      const nonfinite={...result,frame:{...result.frame!,values:result.frame!.values.map(values=>values.map(value=>typeof value==="number"?Infinity:value)),health:result.frame!.health?{...result.frame!.health,error_rate:Infinity,total_spans:Infinity,error_trend:[Infinity,1]}:undefined}};
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={nonfinite}/></MantineProvider>));
      expect(container.textContent).not.toMatch(/NaN|Infinity/);
      await act(async()=>root.render(<MantineProvider><PanelCard {...props} loading={false} result={result}/></MantineProvider>));
      expect(container.textContent).not.toMatch(/NaN|Infinity/);
      if (viz === "logs") expect(container.textContent).toContain("failed");
      if (viz === "log_patterns") {
        expect(container.textContent).toContain("failed <*>");
        expect(container.querySelector('svg[aria-label="Pattern count trend"]')).not.toBeNull();
      }
      if (viz === "traces") {
        expect(container.textContent).toContain("cart");
        expect(container.textContent).toContain("abc");
      }
    }
    await act(async()=>root.unmount());container.remove();
  });
});

it("formats distinct scatter axis units and numeric bucket order",()=>{
 const panel:Panel={id:"p",title:"Rows versus latency",viz:"scatter",x_unit:"count",unit:"ms",query:{from:"spans",by:["service"],measures:["count()","p95(duration_ms)"]}};
 const option=analysisOption(panel,resultFor("scatter"),chartThemeFor(false)) as {xAxis:{name:string;axisLabel:{formatter:(n:number)=>string}};yAxis:{name:string;axisLabel:{formatter:(n:number)=>string}}};
 expect(option.xAxis.name).toBe("count");expect(option.yAxis.name).toBe("ms");expect(option.xAxis.axisLabel.formatter(2)).toBe("2");expect(option.yAxis.axisLabel.formatter(50)).toContain("ms");
 const frame={columns:fixtures.histogram!.columns,values:[[10,null,2],[20,1,10],[1,2,3]],rows:3};
 for(const viz of ["histogram","heatmap"] as const){
  const f=viz==="heatmap"?{columns:[col("time","time","time"),...frame.columns],values:[[3000,1000,2000],...frame.values],rows:3}:frame;
  const got=analysisOption({...panel,viz}, {id:"p",status:"ok",elapsed_ms:1,frame:f},chartThemeFor(false)) as {xAxis?:{data?:string[]};yAxis?:{data?:string[]}};
  expect(viz==="heatmap"?got.yAxis?.data:got.xAxis?.data).toEqual(["−∞–1 ms","2–10 ms","10–20 ms"]);
 }
});

it.each(["histogram","heatmap"] as const)("formats %s duration buckets in shared scaled units", viz => {
 const frame = {columns:fixtures.histogram!.columns,values:[[128,16384,131072,262144],[256,32768,262144,null],[3,4,5,6]],rows:4};
 const f = viz === "heatmap" ? {columns:[col("time","time","time"),...frame.columns],values:[[1000,1000,1000,1000],...frame.values],rows:4} : frame;
 const option = analysisOption({id:"p",title:"Latency",viz},{...resultFor(viz),frame:f},chartThemeFor(false)) as {xAxis:{data?:string[];name?:string};yAxis:{data?:string[]}};
 expect(viz === "heatmap" ? option.yAxis.data : option.xAxis.data).toEqual(["128–256 ms","16–33 s","2.2–4.4 min","≥ 4.4 min"]);
 if (viz === "histogram") expect(option.xAxis.name).toBeUndefined();
});

it.each(["histogram","heatmap"] as const)("preserves distinct %s buckets when rounded labels coincide", viz => {
 const frame = {columns:fixtures.histogram!.columns,values:[[16384,16385],[16385,16386],[3,4]],rows:2};
 const f = viz === "heatmap" ? {columns:[col("time","time","time"),...frame.columns],values:[[1000,1000],...frame.values],rows:2} : frame;
 const option = analysisOption({id:"p",title:"Latency",viz},{...resultFor(viz),frame:f},chartThemeFor(false)) as {series:{data:{value:number|number[];selection:{bucket:{lower:number;upper:number}}}[]}[]};
 const data = option.series[0].data;
 expect(data).toHaveLength(2);
 expect(data.map(point=>point.selection.bucket)).toEqual([{lower:16384,upper:16385},{lower:16385,upper:16386}]);
 expect(data.map(point=>Array.isArray(point.value)?point.value[2]:point.value)).toEqual([3,4]);
 if (viz === "heatmap") expect(data.map(point=>(point.value as number[])[1])).toEqual([0,1]);
});

it("caps heatmap colour at p99 and shows a compact continuous count scale", () => {
 const counts = [...Array(99).fill(10),100000];
 const frame = {columns:fixtures.heatmap!.columns,values:[counts.map((_,i)=>1000+i*60000),counts.map(()=>128),counts.map(()=>256),counts],rows:100};
 const option = analysisOption({id:"p",title:"Latency heatmap",viz:"heatmap"},{...resultFor("heatmap"),frame},chartThemeFor(false)) as {visualMap:{show:boolean;type:string;max:number;orient:string;itemWidth:number};legend?:{show:boolean}};
 expect(option.visualMap.max).toBeLessThanOrEqual(10);
 expect(option.visualMap.show).toBe(true);
 expect(option.visualMap.type).toBe("continuous");
 expect(option.visualMap.orient).toBe("horizontal");
 expect(option.visualMap.itemWidth).toBeLessThanOrEqual(10);
});

it("keeps heatmap and timeline time labels from running together", () => {
 for (const viz of ["heatmap","state_timeline"] as const) {
  const option = analysisOption({id:"p",title:"t",viz},resultFor(viz),chartThemeFor(false)) as {xAxis:{axisLabel:{hideOverlap?:boolean}}};
  expect(option.xAxis.axisLabel.hideOverlap).toBe(true);
 }
});

it("hides single-series legends and retains legends for split series", () => {
 for (const viz of ["histogram","heatmap","scatter"] as const) {
  const option = analysisOption({id:"p",title:viz,viz},resultFor(viz),chartThemeFor(false)) as {legend?:{show:boolean}};
  expect(option.legend?.show ?? false).toBe(false);
 }
 const time = timeseriesOption({id:"p",title:"Rate",viz:"timeseries"},{...resultFor("heatmap"),frame:{columns:[col("time","time","time"),col("count","number","measure")],values:[[1000],[10]],rows:1}},chartThemeFor(false)) as {legend:{show:boolean}};
 expect(time.legend.show).toBe(false);
 const frame = {columns:[col("service","string","dimension"),...fixtures.histogram!.columns],values:[["cart","checkout"],[1,1],[2,2],[3,4]],rows:2};
 const split = analysisOption({id:"p",title:"Buckets",viz:"histogram"},{...resultFor("histogram"),frame},chartThemeFor(false)) as {legend:{show:boolean}};
 expect(split.legend.show).toBe(true);
});

it("formats midnight ticks as local month and day across time charts", () => {
 const midnight = new Date(2026,9,6,0,0,0).getTime();
 const daytime = new Date(2026,9,6,13,45,0).getTime();
 const expected = new Date(midnight).toLocaleDateString(undefined,{month:"short",day:"numeric"});
 const panel: Panel = {id:"p",title:"Time",viz:"timeseries"};
 const options = [timeseriesOption(panel,resultFor("heatmap"),chartThemeFor(false)),...(["heatmap","state_timeline"] as const).map(viz=>analysisOption({...panel,viz},resultFor(viz),chartThemeFor(false)))];
 for (const option of options as {xAxis:{axisLabel?:{formatter?:(n:number)=>string}}}[]) {
  expect(option.xAxis.axisLabel?.formatter?.(midnight)).toBe(expected);
  expect(option.xAxis.axisLabel?.formatter?.(daytime)).toBe("13:45");
 }
});


it("keeps an aria summary and an Inspect data table reachable for every chart", async () => {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  function Harness({ panel, result }: { panel: Panel; result: PanelResult }) {
    const [inspecting, setInspecting] = useState(false);
    return <MantineProvider>
      <PanelCard panel={panel} title="Window summary" result={result} loading={false} height={300} group="g" editing={false} agentAvailable={false}
        onView={() => undefined} onInspect={() => setInspecting(true)} onCopyLink={() => undefined} onExplain={() => undefined} />
      <InspectDrawer panel={inspecting ? panel : undefined} result={result} onClose={() => setInspecting(false)} />
    </MantineProvider>;
  }
  try {
    for (const viz of types) {
      const panel: Panel = { id: "p", title: viz, viz };
      const result = resultFor(viz);
      await act(async () => root.render(<Harness key={viz} panel={panel} result={result} />));
      const summary = container.querySelector(viz === "health" || rowTypes.has(viz) ? '[role="region"]' : '[role="img"]');
      expect(summary?.getAttribute("aria-label")).toContain(viz === "health" ? "Service health" : "Window summary");
      expect(summary?.getAttribute("aria-label")).toContain(viz === "health" ? "services" : `${result.frame!.rows} rows`);
      await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Window summary menu"]')!.click());
      const inspect = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(item => item.textContent?.includes("Inspect"));
      expect(inspect).toBeDefined();
      await act(async () => inspect!.click());
      const table = document.querySelector('[role="dialog"] table');
      expect(table).not.toBeNull();
      expect([...table!.querySelectorAll("th")].map(header => header.textContent)).toEqual(result.frame!.columns.map(column => column.name));
      expect(table!.querySelectorAll("tbody tr")).toHaveLength(result.frame!.rows);
    }
  } finally {
    await act(async () => root.unmount());
    container.remove();
  }
});

it("preserves literal dimension keys and sanitizes rows without losing selections", () => {
  const frame = { columns: [col("start", "time", "time"), col("http.route", "string", "dimension"), col("trace_id", "string", "dimension"), col("namespace", "string", "dimension"), col("count", "number", "measure")],
    values: [[1000], ["/checkout"], ["trace-1"], ["shop"], [NaN]], rows: 1 };
  const panel: Panel = { id: "p", title: "Rows", viz: "table", query: { from: "spans", by: ["attributes['http.route']"] } };
  const model = rowModel(panel, { id: "p", status: "ok", elapsed_ms: 1, frame });
  expect(frameRows(frame)[0].count).toBeNull();
  expect(frame.values[4][0]).toBeNaN();
  expect(model.selection(model.rows[0])).toEqual({ time: 1000, dimensions: { "attributes['http.route']": "/checkout" }, trace_id: "trace-1", namespace: "shop" });
});

it("highlights log bodies and opens captured trace links without activating the row twice", async () => {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const onPoint = vi.fn();
  try {
    await act(async () => root.render(<MantineProvider><Viz panel={{ id: "p", title: "Logs", viz: "logs", options: { highlight: "failed" } }} result={resultFor("logs")} dark={false} height={200} group="g" onPoint={onPoint} /></MantineProvider>));
    expect(container.querySelector("mark")?.textContent).toBe("failed");
    expect(container.querySelector('[data-trace-id="abc"]')?.textContent).toBe("abc");
    expect(container.querySelector('[data-trace-id="abc"]')?.closest("a")).not.toBeNull();
    expect(container.querySelector("tbody button")).toBeNull();
    expect(container.querySelector('[data-trace-id="abc"]')?.getAttribute("aria-label")).toBe("Trace ID abc");
    expect(container.querySelector(".mantine-Badge-root")?.textContent).toBe("ERROR");
    expect(onPoint).not.toHaveBeenCalled();
    const link = container.querySelector<HTMLAnchorElement>('[data-trace-id="abc"]')!;
    const target = JSON.parse(new URL(link.href).searchParams.get("drill")!);
    expect(target.trace_id).toBe("abc");
    expect(target.namespace).toBe("shop");
    expect(target.window_from).toBe(new Date(0).toISOString());
    expect(target.window_to).toBe(new Date(10000).toISOString());
    await act(async () => link.click());
    expect(onPoint.mock.calls).toEqual([[{ time: 1000, dimensions: {}, trace_id: "abc", namespace: "shop" }]]);
  } finally {
    await act(async () => root.unmount()); container.remove();
  }
});

it("selects the service exactly once after sorting trace rows and updating callbacks", async () => {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const first = vi.fn(); const next = vi.fn();
  const frame = { ...fixtures.traces!, values: [["abc", "def"], ["shop", "ops"], ["checkout", "payment"], ["cart", "charge"], [50, 10], ["STATUS_CODE_ERROR", "OK"], [1000, 2000]], rows: 2 };
  const props = { panel: { id: "p", title: "Traces", viz: "traces" as const }, result: { ...resultFor("traces"), frame }, dark: false, height: 200, group: "g" };
  try {
    await act(async () => root.render(<MantineProvider><Viz {...props} onSelect={first} /></MantineProvider>));
    await act(async () => container.querySelector<HTMLButtonElement>("th:nth-child(5) button")!.click());
    await act(async () => container.querySelector<HTMLButtonElement>("th:nth-child(5) button")!.click());
    expect(container.querySelector("th:nth-child(5)")?.getAttribute("aria-sort")).toBe("ascending");
    expect(container.querySelector("tbody tr [data-trace-id]")?.getAttribute("data-trace-id")).toBe("def");
    await act(async () => root.render(<MantineProvider><Viz {...props} onSelect={next} /></MantineProvider>));
    const service = container.querySelector<HTMLButtonElement>("tbody tr button")!;
    await act(async () => service.click());
    expect(next.mock.calls).toEqual([["payment"]]);
    expect(first).not.toHaveBeenCalled();
    await act(async () => service.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(next).toHaveBeenCalledTimes(1);
  } finally {
    await act(async () => root.unmount()); container.remove();
  }
});

it("labels pattern points with effective frame timing and retains indexes across invalid counts", async () => {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const props = { panel: { id: "p", title: "Patterns", viz: "log_patterns" as const, query: { from: "logs" as const, bucket: "1m" } }, dark: false, height: 200, group: "g" };
  try {
    const frame = { ...fixtures.log_patterns!, trend: { start_ms: -120000, step_ms: 120000 }, values: [["failed <*>"], [4], ["[1,null,1e309,3]"]] };
    await act(async () => root.render(<MantineProvider><Viz {...props} result={{ ...resultFor("log_patterns"), interval: "2m", frame }} /></MantineProvider>));
    const trend = container.querySelector('svg[aria-label="Pattern count trend"]')!;
    expect([...trend.querySelectorAll("circle title")].map(title => title.textContent)).toEqual(["1969-12-31T23:58:00.000Z: 1", "1970-01-01T00:04:00.000Z: 3"]);
    expect(trend.outerHTML).not.toMatch(/NaN|Infinity/);
    for (const value of ["invalid", "{}", "[]", "[null,1e309]"]) {
      await act(async () => root.render(<MantineProvider><Viz {...props} result={{ ...resultFor("log_patterns"), frame: { ...frame, values: [["failed <*>"], [0], [value]] } }} /></MantineProvider>));
      expect(container.querySelector("svg polyline")?.getAttribute("points")).toBe("");
      expect(container.innerHTML).not.toMatch(/NaN|Infinity/);
    }
  } finally {
    await act(async () => root.unmount()); container.remove();
  }
});

it("aligns split histogram series and preserves numeric bucket selections", () => {
  const frame = { columns: [col("service", "string", "dimension"), ...fixtures.histogram!.columns], values: [["cart", "checkout", "cart"], [10, null, 2], [20, 1, 10], [1, 2, 3]], rows: 3 };
  const panel: Panel = { id: "p", title: "Buckets", viz: "histogram", query: { from: "spans", by: ["service"] } };
  const option = analysisOption(panel, { id: "p", status: "ok", elapsed_ms: 1, frame }, chartThemeFor(false)) as { series: { name: string; data: { value: number; selection: unknown }[] }[] };
  expect(option.series.map(series => series.data.map(point => point.value))).toEqual([[0, 3, 1], [2, 0, 0]]);
  expect(option.series[0].data[1].selection).toEqual({ dimensions: { service: "cart" }, bucket: { lower: 2, upper: 10 } });
});

it("filters nonpositive scatter points on either logarithmic axis", () => {
  const frame = { ...fixtures.scatter!, values: [["zero", "negative", "valid"], [0, -2, 2], [10, 0, 50]], rows: 3 };
  const result = { ...resultFor("scatter"), frame };
  for (const options of [{ x_scale: "log" as const }, { y_scale: "log" as const }, { scale: "log" as const }]) {
    const option = analysisOption({ id: "p", title: "Points", viz: "scatter", options }, result, chartThemeFor(false)) as { series: { data: { value: number[] }[] }[] };
    expect(option.series[0].data.every(point => (options.y_scale ? point.value[1] : point.value[0]) > 0)).toBe(true);
    expect(option.series[0].data.some(point => point.value[0] === 2 && point.value[1] === 50)).toBe(true);
  }
});

it("renders timeline states with health shapes and distinguishes unknown cells", () => {
  const panel: Panel = { id: "p", title: "States", viz: "state_timeline", thresholds: [{ value: 10, status: "warn" }, { value: 20, status: "bad" }], query: { from: "spans", by: ["service"] } };
  const frame = { ...fixtures.state_timeline!, values: [[1000, 2000, 3000, 4000], ["cart", "cart", "cart", "cart"], [null, 5, 15, 25]], rows: 4 };
  type Shape = { type: string; style: { lineDash?: number[] }; shape: Record<string, unknown>; children?: Shape[] };
  const option = analysisOption(panel, { ...resultFor("state_timeline"), interval: "1s", frame }, chartThemeFor(false)) as { series: { data: { value: number[] }[]; renderItem: (params: unknown, api: unknown) => Shape }[] };
  const series = option.series[0];
  const shapes = series.data.map(point => series.renderItem({}, { value: (index: number) => point.value[index], coord: (value: number[]) => value, size: () => [1, 20], style: () => ({}) }).children![1]);
  expect(shapes.map(shape => shape.type)).toEqual(["circle", "circle", "rect", "polygon"]);
  expect(shapes[0].style.lineDash).toEqual([2, 2]);
  expect(shapes[1].style.lineDash).toBeUndefined();
  expect(series.data.map(point => point.value[3])).toEqual([2000, 3000, 4000, 5000]);
});

it("grades timeline cells by the spec direction, then the result's inferred direction", () => {
  const panel: Panel = { id: "p", title: "States", viz: "state_timeline", thresholds: [{ value: 10, status: "warn" }, { value: 20, status: "bad" }], query: { from: "spans", by: ["service"] } };
  const frame = { ...fixtures.state_timeline!, values: [[1000, 2000, 3000], ["cart", "cart", "cart"], [5, 15, 25]], rows: 3 };
  type Shape = { type: string; children?: Shape[] };
  const shapes = (p: Panel, better?: "lower" | "higher") => {
    const option = analysisOption(p, { ...resultFor("state_timeline"), interval: "1s", frame, better }, chartThemeFor(false)) as { series: { data: { value: number[] }[]; renderItem: (params: unknown, api: unknown) => Shape }[] };
    const series = option.series[0];
    return series.data.map(point => series.renderItem({}, { value: (index: number) => point.value[index], coord: (value: number[]) => value, size: () => [1, 20], style: () => ({}) }).children![1].type);
  };
  const lower = shapes(panel);
  expect(shapes(panel, "higher")).not.toEqual(lower);
  expect(shapes({ ...panel, better: "lower" }, "higher")).toEqual(lower);
});

it("reuses shaped service-map nodes, selects services and includes edge-only endpoints", () => {
  for (const dark of [false, true]) {
    const option = analysisOption({ id: "p", title: "Map", viz: "service_map" }, resultFor("service_map"), chartThemeFor(dark)) as { series: { data: { name: string; symbol: string; selection: unknown; itemStyle: { borderType: string } }[] }[] };
    const nodes = option.series[0].data;
    expect(nodes.map(node => node.name)).toEqual(["checkout", "payment"]);
    expect(nodes[0].symbol).toBe(healthSymbol("unhealthy"));
    expect(nodes[0].selection).toEqual({ dimensions: { service: "checkout" } });
    expect(nodes[1].itemStyle.borderType).toBe("dashed");
  }
});

it("keeps original table indexes after sorting and sanitizes fallback cells", async () => {
  const frame = { columns: [col("service", "string", "dimension"), col("count", "number", "measure")], values: [["checkout", "cart"], [Infinity, 3]], rows: 2 };
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const renderCell = vi.fn(({ column, rowIndex }: { column: { name: string }; rowIndex: number }) => column.name === "service" ? <span data-original-row={rowIndex}>Link {rowIndex}</span> : undefined);
  try {
    await act(async () => root.render(<MantineProvider><TableViz panel={{ id: "p", title: "Rows", viz: "table" }} result={{ id: "p", status: "ok", elapsed_ms: 1, frame }} height={200} renderCell={renderCell} /></MantineProvider>));
    expect(container.textContent).not.toMatch(/NaN|Infinity/);
    expect(container.querySelector("tbody tr td:nth-child(2)")!.textContent).toBe("—");
    await act(async () => container.querySelector<HTMLButtonElement>("th:nth-child(2) button")!.click());
    expect(container.querySelector("tbody tr [data-original-row]")!.getAttribute("data-original-row")).toBe("1");
    expect(renderCell.mock.calls.some(([cell]) => cell.rowIndex === 0)).toBe(true);
  } finally {
    await act(async () => root.unmount()); container.remove();
  }
});


it("renders populated and empty analysis options with the real ECharts engine", () => {
  use([SVGRenderer, BarChart, CustomChart, GraphChart, ScatterChart, GridComponent, TooltipComponent, VisualMapComponent]);
  for (const dark of [false, true]) for (const viz of types.filter(viz => viz !== "health" && !rowTypes.has(viz))) {
    const panel: Panel = { id: "p", title: viz, viz, thresholds: [{ value: 1, status: "bad" }] };
    const result = resultFor(viz);
    for (const frame of [result.frame!, { ...result.frame!, values: result.frame!.columns.map(() => []), rows: 0 }]) {
      const chart = init(null, undefined, { renderer: "svg", ssr: true, width: 640, height: 300 });
      try {
        chart.setOption(analysisOption(panel, { ...result, frame }, chartThemeFor(dark)), { notMerge: true });
        const svg = chart.renderToSVGString();
        expect(svg).toContain("<svg");
        expect(svg).not.toMatch(/NaN|Infinity/);
      } finally {
        chart.dispose();
      }
    }
  }
});
