import { expect, it } from "vitest";
// @ts-expect-error Node-only test helpers; browser TS config excludes Node globals.
import { readFileSync, existsSync } from "node:fs";
import { chartThemeFor, gaugeOption, timeseriesOption } from "../../../panels/compile";
import { withAnnotations } from "../../../panels/annotations";
import type { Frame } from "../../../panels/types";
it.each([{width:220,height:140},{width:500,height:300},{width:159,height:56}])("I3 renders only a linear meter at %o", size => {
  const option = gaugeOption({id:"g",title:"G",viz:"gauge",min:0,max:100,thresholds:[{value:50,status:"warn"}]},75,chartThemeFor(false),undefined,size) as any;
  expect(option.series).toEqual([]);
  const graphics = option.graphic;
  expect(graphics.find((g:any) => g.id === "gauge-value").style.fontSize).toBeGreaterThanOrEqual(28);
  expect(graphics.find((g:any) => g.id === "gauge-track").shape.height).toBe(10);
  expect(graphics.find((g:any) => g.id === "gauge-fill").shape.width).toBe((size.width-8)*.75);
  expect(graphics.find((g:any) => g.id === "gauge-marker")).toBeDefined();
  const bands = graphics.filter((g:any)=>g.type === "rect" && !g.id);
  expect(bands.map((g:any)=>[g.shape.x,g.shape.width])).toEqual([[4,(size.width-8)/2],[4+(size.width-8)/2,(size.width-8)/2]]);
  expect(bands[0].shape.r).toEqual([5,0,0,5]);
  expect(bands[1].shape.r).toEqual([0,5,5,0]);
  expect(graphics.filter((g:any)=>g.type === "text").map((g:any)=>g.style.text)).toEqual(expect.arrayContaining(["0","100"]));
});
it("M2 draws only current named overlays and matches the counted Other label", () => {
  const f = (names:string[]):Frame => ({rows:names.length,columns:[{name:"time",type:"time",role:"time"},{name:"service",type:"string",role:"dimension"},{name:"count",type:"number",role:"measure"}],values:[names.map(()=>1000),names,names.map(()=>1)]});
  const option = timeseriesOption({id:"p",title:"P",viz:"timeseries"},{id:"p",status:"ok",elapsed_ms:1,frame:f(["A","B","Other (5)"]),previous:f(["X","B","Other (4)"]),shift_ms:1000},chartThemeFor(false)) as any;
  expect(option.series.map((s:any)=>s.name)).toEqual(["A","B","Other (5)","B · previous","Other (5) · previous"]);
});
it("M4 removes superseded registrations, status chip and invisible annotation geometry", () => {
  expect(existsSync("src/dashboards/status-chip.tsx")).toBe(false);
  const canvas = readFileSync("src/dashboards/echart-canvas.tsx","utf8");
  expect(canvas).not.toMatch(/GraphChart|GaugeChart|anomalyNames/);
  const option = withAnnotations({series:[{type:"line"}],grid:{}},{id:"p",title:"P",viz:"timeseries"},{id:"p",status:"ok",elapsed_ms:1,from_ms:0,to_ms:10000},{deploys:[{namespace:"",service:"A",version:"v",at:new Date(1000).toISOString()}],anomalies:[]},{},chartThemeFor(false)) as any;
  expect(option.series[0].markLine.data[0].label).toEqual({show:false});
});
