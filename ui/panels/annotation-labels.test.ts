import { withAnnotations } from "./annotations";
import { chartThemeFor, timeseriesOption } from "./compile";
import { wrappingLegend } from "./series";

it.each([false, true])("uses a separate annotation lane when the first legend row fills the canvas (dark=%s)", dark => {
  const size = { width: 450, height: 248, measureText: (text: string) => text.length * 6 };
  const names = ["cart", "currency", "flagd", "frontend", "frontend-proxy", "product-catalog", "Other (9)"];
  const theme = chartThemeFor(dark);
  const legend = wrappingLegend(names, size.width, true, theme.muted, theme.font, size.measureText);
  const base = { legend: legend.option, grid: { left: 8, right: 151, top: legend.top }, series: [{ type: "line" }] };
  const option = withAnnotations(base, { id: "p", title: "Requests", viz: "timeseries" }, { id: "p", status: "ok", elapsed_ms: 0, from_ms: 0, to_ms: 10000 }, {
    deploys: [], anomalies: [{ namespace: "shop", service: "cart", kind: "latency", title: "Slow", severity: "warn", from: new Date(2000).toISOString(), to: new Date(9000).toISOString() }],
  }, {}, theme, size) as any;
  // Six measured entries fit the real first row; Other wraps to the second.
  const firstRow = names.slice(0, 6).reduce((used, name) => used + 15 + size.measureText(name) + (used ? 6 : 0), 0);
  expect(firstRow).toBe(444);
  const chips = option.graphic.filter((g: any) => g.annotation);
  expect(chips.map((g: any) => g.style.text)).toEqual(["anomaly"]);
  expect(option.grid.top).toBe(base.grid.top + 18);
  expect(chips[0].top).toBeGreaterThanOrEqual(base.grid.top);
});

it("keeps shared-row chips clear of a one-row legend with end labels at 560 px", () => {
  const size = { width: 560, height: 248, measureText: (text: string) => text.length * 6 };
  const panel = { id: "p", title: "Requests", viz: "timeseries" as const };
  const names = ["cart", "frontend-proxy", "recommendation", "currency"];
  const result = { id: "p", status: "ok" as const, elapsed_ms: 0, from_ms: 0, to_ms: 10000, frame: {
    columns: [{ name: "time", type: "time" as const, role: "time" as const }, { name: "service", type: "string" as const, role: "dimension" as const }, { name: "count", type: "number" as const, role: "measure" as const }],
    values: [[...names.map(() => 0), ...names.map(() => 10000)], [...names, ...names], [1, 2, 3, 4, 2, 3, 4, 5]], rows: 8,
  } };
  const theme = chartThemeFor(false);
  const base = timeseriesOption(panel, result, theme, size) as any;
  expect(base.grid.right).toBeGreaterThan(16);
  expect(base.series.every((s: any) => s.endLabel.show)).toBe(true);
  const option = withAnnotations(base, panel, result, { deploys: [{ namespace: "shop", service: "cart", version: "2.3.0", at: new Date(1000).toISOString() }], anomalies: [{ namespace: "shop", service: "cart", kind: "latency", title: "Slow", severity: "warn", from: new Date(2000).toISOString(), to: new Date(9000).toISOString() }] }, {}, theme, size) as any;
  const chips = option.graphic.filter((g: any) => g.annotation);
  expect(chips).toHaveLength(2);
  expect(option.grid.top).toBe(base.grid.top);
  let legendRight = 0;
  for (const name of base.legend.data) legendRight += 15 + size.measureText(base.legend.formatter?.(name) ?? name) + (legendRight ? base.legend.itemGap ?? 6 : 0);
  for (const chip of chips) expect(size.width - chip.right - chip.style.width - 6).toBeGreaterThanOrEqual(legendRight + 12);
  expect(chips[0].right).toBe(16);
});

it.each([false, true])("merges six adjacent windows and labels the anomaly row once with its source count (dark=%s)", dark => {
  const anomalies = Array.from({length:6},(_,i)=>({namespace:"shop",service:"cart",kind:"volume",title:"Changed",severity:"warn",from:new Date(i*1000).toISOString(),to:new Date((i+1)*1000).toISOString()}));
  const option=withAnnotations({series:[{type:"line"}]},{id:"p",title:"P",viz:"timeseries"},{id:"p",status:"ok",elapsed_ms:0,from_ms:0,to_ms:10000},{deploys:[],anomalies},{},chartThemeFor(dark)) as any;
  const areas=option.series[0].markArea.data;
  expect(areas).toHaveLength(1);expect(areas[0].map((a:any)=>a.xAxis)).toEqual([0,6000]);
  const labels=option.graphic.filter((g:any)=>g.annotation);
  expect(labels.map((g:any)=>g.style.text)).toEqual(["anomaly"]);
  expect(labels[0].tooltip.formatter()).toContain("6 windows");
  expect(areas[0][0].tooltip.formatter()).toContain("6 windows");
});

it("uses one anomaly label across six service scopes and drops colliding deploy labels without removing markers",()=>{
  const anomalies=Array.from({length:6},(_,i)=>({namespace:"shop",service:`svc-${i}`,kind:"latency",title:"Slow",severity:"bad",from:new Date(1000).toISOString(),to:new Date(9000).toISOString()}));
  const deploys=Array.from({length:6},(_,i)=>({namespace:"shop",service:`svc-${i}`,version:"v2",at:new Date(1000+i).toISOString()}));
  const option=withAnnotations({series:[{type:"line"}],grid:{left:8,right:8}},{id:"p",title:"P",viz:"timeseries"},{id:"p",status:"ok",elapsed_ms:0,from_ms:0,to_ms:10000},{deploys,anomalies},{},chartThemeFor(false),{width:360,height:248,measureText:text=>text.length*6}) as any;
  expect(option.graphic.filter((g:any)=>g.style.text==="anomaly")).toHaveLength(1);
  expect(option.series[0].markLine.data).toHaveLength(6);
  const labels=option.graphic.filter((g:any)=>g.annotation);
  expect(labels.some((g:any)=>g.style.text.includes("deploys"))).toBe(false);
  expect(labels.length).toBeLessThan(7);
  const boxes=labels.map((g:any)=>({right:g.right,left:g.right+g.style.width+6}));
  for(let i=1;i<boxes.length;i++)expect(boxes[i].right).toBeGreaterThanOrEqual(boxes[i-1].left);
  expect(boxes.at(-1).left).toBeLessThanOrEqual(360-8);
});
