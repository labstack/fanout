import { describe, expect, it } from "vitest";
import { timeseriesOption, chartThemeFor } from "../../../panels/compile";
import { withAnnotations } from "../../../panels/annotations";
import type { Panel, PanelResult } from "../../../panels/types";

const panel: Panel = { id: "x", title: "Latency", viz: "timeseries", unit: "ms", thresholds: [{ value: 1500, status: "bad", label: "p99 budget" }] };
const result: PanelResult = { id: "x", status: "ok", elapsed_ms: 1, from_ms: 0, to_ms: 3600000, frame: { columns: [{ name: "time", type: "time", role: "time" }, ...["p50", "p95", "p99"].map(name => ({ name, type: "number" as const, role: "measure" as const, unit: "ms" }))], values: [[0, 3599999], [500, 500], [700, 700], [701, 701]], rows: 2 } };
type Series = { endLabel?: { show: boolean; formatter: string; color: string; fontSize: number }; labelLayout: { moveOverlap: string }; lineStyle: { width: number }; showSymbol: boolean; markLine: { data: { xAxis?: number; label: { show: boolean; formatter: string; rotate?: number; position: string; textBorderColor?: string; fontSize: number } }[] }; markArea: { data: { label: { show: boolean; formatter: string; position: string }; tooltip: { formatter(): string }; itemStyle: { opacity: number } }[][] } };
describe("timeseries end labels", () => {
  it.each([false, true])("uses collision-managed direct end labels and formatted threshold labels (%s)", dark => {
    const theme = chartThemeFor(dark); const option = timeseriesOption(panel, result, theme);
    expect((option.grid as { right: number }).right).toBeGreaterThanOrEqual(48);
    for (const series of option.series as Series[]) {
      expect(series.lineStyle.width).toBe(2); expect(series.showSymbol).toBe(false);
      expect(series.endLabel).toMatchObject({ show: true, color: theme.muted, fontSize: 12, formatter: "{a}" });
      expect(series.labelLayout).toEqual({ dy: expect.any(Number), hideOverlap: true });
      expect(series.endLabel).toMatchObject({ lineHeight: 14, padding: [1, 0] });
    }
    expect((option.series as Series[])[0].markLine.data[0].label).toMatchObject({ formatter: "p99 budget 1.5 s", position: "insideStartTop", fontSize: 12 });
  });
  it("omits end labels for more than six source series", () => {
    const columns = Array.from({ length: 7 }, (_, i) => ({ name: `s${i}`, type: "number" as const, role: "measure" as const }));
    const o = timeseriesOption(panel, { ...result, frame: { columns: [result.frame!.columns[0], ...columns], values: [[0], ...columns.map(() => [1])], rows: 1 } }, chartThemeFor(false));
    expect((o.series as Series[]).every(s => !s.endLabel?.show)).toBe(true);
  });
  it.each([false, true])("merges deploys within 12px and places deploy/anomaly labels in the top lane (%s)", dark => {
    const theme = chartThemeFor(dark), base = timeseriesOption(panel, result, theme);
    const got = withAnnotations(base, panel, result, { deploys: [600000, 610000, 1800000].map(at => ({ namespace: "shop", service: "payments", version: "v2.14.0", at: new Date(at).toISOString() })), anomalies: [{ namespace: "shop", service: "payments", title: "Slow", severity: "bad", kind: "latency", from: new Date(800000).toISOString(), to: new Date(2000000).toISOString() }] }, {}, theme, { width: 500, height: 248 });
    expect((got.grid as {top:number}).top-(base.grid as {top:number}).top).toBeLessThanOrEqual(18);
    const first = (got.series as Series[])[0]; const deploys = first.markLine.data.filter(mark => mark.xAxis !== undefined);
    expect(deploys).toHaveLength(2);
    expect((got.graphic as {annotation?:boolean;style:{text:string}}[]).filter(g=>g.annotation).map(g=>g.style.text)).toEqual(["2 deploys","payments v2.14.0","anomaly"]);
    for (const deploy of deploys) expect(deploy.label).toEqual({show:false});
    const area = first.markArea.data[0][0]; expect(area.label).toEqual({show:false});
    expect(area.tooltip.formatter()).toContain("Slow · bad"); expect(area.tooltip.formatter()).toContain(new Date(800000).toISOString()); expect(area.tooltip.formatter()).toContain(new Date(2000000).toISOString());
    expect(area.itemStyle.opacity).toBeLessThanOrEqual(.12);
  });
});

it("reserves the measured longest label lane for three endpoints within two percent",()=>{
 const clustered={...result,frame:{...result.frame!,values:[[0,3599999],[900,900],[901,901],[902,902]]}};
 const o=timeseriesOption(panel,clustered,chartThemeFor(false),{width:1400,height:800,measureText:()=>120}) as any;
 expect(o.grid.right).toBe(156);
 for(const line of o.series){expect(line.endLabel.width).toBe(132);expect(line.endLabel.lineHeight).toBe(14);expect(line.labelLayout).toEqual({dy:expect.any(Number),hideOverlap:true});}
 expect(o.legend.show).toBe(true);
});

it.each(["p50 latency", "a".repeat(24), "a".repeat(30)])("keeps end-label metric slack up to the 24-character cap (%s)", name => {
 const names=[name,"p95 latency"];
 const frame={columns:[result.frame!.columns[0],...names.map(name=>({name,type:"number" as const,role:"measure" as const,unit:"ms"}))],values:[[0,3599999],[900,900],[901,901]],rows:2};
 const measureText=(text:string)=>Array.from(text).length*7;
 const o=timeseriesOption(panel,{...result,frame},chartThemeFor(false),{width:1400,height:800,measureText}) as any;
 const measured=Math.max(...names.map(n=>measureText(Array.from(n).slice(0,24).join(""))));
 const label=o.series[0].endLabel;
 expect(label.width).toBe(measured+12);
 expect(o.grid.right).toBe(label.width+24);
 expect(label).toMatchObject({overflow:"truncate",ellipsis:"…"});
 // Allow a metric discrepancy without truncating names through the cap;
 // longer names exceed the ECharts truncate box even with the same slack.
 expect(measureText(name)+2<=label.width).toBe(name.length<=24);
});

it("drops lower-priority end labels when one padded line fits and retains the legend",()=>{
 const o=timeseriesOption(panel,result,chartThemeFor(false),{width:1400,height:68}) as any;
 expect(o.series.map((s:any)=>s.endLabel.show)).toEqual([true,false,false]);expect(o.legend.show).toBe(true);
});

it.each([120,170,400])("bounds clustered end labels inside a %ipx plot without collisions",height=>{
 const clustered={...result,frame:{...result.frame!,values:[[0,3599999],[900,900],[901,901],[902,902]]}};
 // At this width the legend consumes 22px, axis labels and bottom another 30px.
 const o=timeseriesOption({...panel,thresholds:[]},clustered,chartThemeFor(false),{width:1400,height:height+52}) as any;
 const labels=o.series.filter((s:any)=>s.endLabel.show).map((s:any)=>({y:o.grid.top+height*(1-s.data.at(-1)[1]/o.yAxis.max)+s.labelLayout.dy,height:s.endLabel.lineHeight+2}));
 expect(labels.length).toBeGreaterThan(0);expect(o.legend.show).toBe(true);
 for(const label of labels){expect(label.y-label.height/2).toBeGreaterThanOrEqual(o.grid.top);expect(label.y+label.height/2).toBeLessThanOrEqual(o.grid.top+height);}
 for(let i=0;i<labels.length;i++)for(let j=i+1;j<labels.length;j++)expect(Math.abs(labels[i].y-labels[j].y)).toBeGreaterThanOrEqual(16);
});

it("retains bounded direct labels on logarithmic axes without explicit extents",()=>{
 const o=timeseriesOption({...panel,options:{scale:"log"}},result,chartThemeFor(false),{width:500,height:248}) as any;
 expect(o.series[0].endLabel.show).toBe(true);expect(Number.isFinite(o.series[0].labelLayout.dy)).toBe(true);
 expect(o.legend.show).toBe(true);
});
