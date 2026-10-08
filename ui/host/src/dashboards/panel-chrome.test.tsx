import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { gaugeOption, chartThemeFor, timeseriesOption } from "../../../panels/compile";
import { analysisOption } from "../../../panels/analysis";
import { heatRamp } from "../../../panels/heat-scale";
import { withAnnotations } from "../../../panels/annotations";
import { relativeLuminance } from "../../../theme";
import { PanelCard } from "./panel-card";
import type { Panel, PanelResult } from "../../../panels/types";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({label}: {label:string}) => <div role="img" aria-label={label}/> }));
const p:Panel={id:"p",title:"Panel",viz:"timeseries",query:{from:"spans"},drill:"traces"};
const r:PanelResult={id:"p",status:"ok",elapsed_ms:1,from_ms:0,to_ms:600000,interval:"1m",frame:{columns:[{name:"time",type:"time",role:"time"},{name:"calls",type:"number",role:"measure"}],values:[[0,60000],[1,2]],rows:2}};
const cleanup:(()=>void)[]=[];
afterEach(async()=>{await act(async()=>cleanup.splice(0).forEach(fn=>fn()));vi.restoreAllMocks();document.body.innerHTML="";});
async function render(panel:Panel=p,result:PanelResult=r,onSelect:((value:string)=>void)|undefined=undefined,vars?:Record<string,string>,onVariable?: (name:string,value:string)=>void) {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanup.push(()=>root.unmount());
  await act(async()=>root.render(<MantineProvider><PanelCard panel={panel} title={panel.title} result={result} loading={false} height={300} group="g" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} onSelect={onSelect} vars={vars} onVariable={onVariable}/></MantineProvider>));
  return host;
}
it.each([120,159,160,238,300])("gauge fills its measured body, readable untruncated value and separate status at %ipx",width=>{
  const height=76, o=gaugeOption({...p,viz:"gauge",min:0,max:10,unit:"percent",thresholds:[{value:1,status:"warn"}]},1.74,chartThemeFor(false),"percent",{width,height}) as any;
  expect(o.series).toEqual([]);const value=o.graphic.find((g:any)=>g.id==="gauge-value");
  expect(value.style.fontSize).toBeGreaterThanOrEqual(24);expect(value.style.text).toBe("1.74%");
  expect(o.graphic.filter((g:any)=>g.type==="text"&&g.style.text.includes("%"))).toHaveLength(3);
});
it("puts the interaction hint in the subtitle and recovers the footer row",async()=>{
  const h=await render();expect(h.querySelector('[data-panel-subtitle]')?.textContent).toBe("time series · spans · click for exemplar traces");
  expect(h.querySelector('[data-panel-hint]')).toBeNull();expect(h.querySelector('[data-panel-notes]')).toBeNull();
});
it.each([160,200,260])("adaptive y ticks use the plot budget at %ipx",height=>{
  const o=timeseriesOption(p,r,chartThemeFor(false),{width:500,height}) as any;
  expect(o.yAxis.splitNumber).toBe(Math.max(2,Math.floor((height-o.grid.top-o.grid.bottom-22)/32)));
  expect(o.yAxis.axisLabel.hideOverlap).toBe(true);
});
it("markers share free first-row legend space, otherwise reserve at most 18px",()=>{
  const theme=chartThemeFor(false),anns={deploys:[{service:"cart",namespace:"",version:"2.3",at:new Date(300000).toISOString()}],anomalies:[]};
  const frame={columns:[...r.frame!.columns,{name:"service",type:"string" as const,role:"dimension" as const}],values:[...r.frame!.values,["a","b"]],rows:2};
  const base=timeseriesOption(p,{...r,frame},theme,{width:500,height:200}) as any;
  const o=withAnnotations(base,p,r,anns,{},theme,{width:500,height:200}) as any;
  expect(o.grid.top).toBe(base.grid.top);expect(o.graphic.some((g:any)=>g.style?.text==="cart 2.3")).toBe(true);
  const lane=withAnnotations(timeseriesOption(p,r,theme),p,r,anns,{},theme) as any;
  expect(lane.grid.top-12).toBeLessThanOrEqual(18);
});
it("labels every fitting timeline row and explicitly reports the rest",()=>{
  const names=Array.from({length:20},(_,i)=>`svc-${i}`),frame={columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"service",type:"string" as const,role:"dimension" as const},{name:"errors",type:"number" as const,role:"measure" as const}],values:[names.map(()=>0),names,names.map(()=>2)],rows:20};
  const o=analysisOption({...p,viz:"state_timeline"},{...r,frame},chartThemeFor(false),{width:500,height:200}) as any;
  expect(o.yAxis.axisLabel.interval).toBe(0);expect(o.yAxis.data.length).toBeLessThan(20);
  expect((200-o.grid.top-o.grid.bottom-22)/o.yAxis.data.length).toBeGreaterThanOrEqual(16);
  expect(o.series[0].data.every((d:any)=>d.value[1]<o.yAxis.data.length)).toBe(true);
  expect(o.graphic.find((g:any)=>g.style?.text===`+${20-o.yAxis.data.length} rows`).tooltip.formatter()).toContain("svc-19");
});
it("text panels keep Spec in the menu with no segmented views",async()=>{
  const h=await render({...p,viz:"text",content:"Hello"});expect(h.querySelector('[role="group"][aria-label="Panel view"]')).toBeNull();
  await act(async()=>h.querySelector<HTMLButtonElement>('[aria-label="Panel menu"]')!.click());
  const spec=document.querySelector<HTMLButtonElement>('[data-panel-view="Spec"]')!;expect(spec).not.toBeNull();
  await act(async()=>spec.click());expect(h.querySelector("pre")).not.toBeNull();
});
it.each([false,true])("retains seven monotonic colours while skewed counts encode log magnitude (%s)",dark=>{
  const counts=[0,...Array.from({length:69},(_,i)=>i+1),1000000000],theme=chartThemeFor(dark);
  const frame={columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"count",type:"number" as const,role:"measure" as const}],values:[counts.map((_,i)=>i*60000),counts],rows:counts.length};
  const o=analysisOption({...p,viz:"heatmap"},{...r,frame},theme) as any;
  const fills=o.series[0].data.map((d:any)=>o.series[0].renderItem({}, {value:(i:number)=>d.value[i],coord:(v:number[])=>[v[0]/1000,20],size:()=>[60,20],style:()=>({})}).style.fill);
  expect(new Set(fills).size).toBe(3);
  const ramp=heatRamp(theme);expect(ramp).toHaveLength(7);
  const l=ramp.map(relativeLuminance);expect(l.every((v,i)=>!i||(dark?v>l[i-1]:v<l[i-1]))).toBe(true);
  const top=l.at(-1)!,surface=relativeLuminance(theme.surface);expect((Math.max(top,surface)+.05)/(Math.min(top,surface)+.05)).toBeGreaterThanOrEqual(3);
  expect(o.visualMap.text).toEqual(["1B","1"]);expect(o.series[0].data.at(-1).value[2]).toBe(1000000000);
});
it("scatter has muted 11px alias titles outside the plot",()=>{
  const frame={...r.frame!,columns:[{name:"operation",type:"string" as const,role:"dimension" as const},{name:"calls",type:"number" as const,role:"measure" as const},{name:"p95",type:"number" as const,role:"measure" as const,unit:"ms"}],values:[["GET"],[100],[50]],rows:1};
  const theme=chartThemeFor(false),o=analysisOption({...p,viz:"scatter"},{...r,frame},theme) as any;
  expect(o.xAxis).toMatchObject({name:"calls",nameLocation:"middle",nameGap:28,nameTextStyle:{fontSize:11,color:theme.muted,align:"right",verticalAlign:"top"}});
  expect(o.yAxis).toMatchObject({name:"p95",nameLocation:"end",nameGap:8,nameRotate:0,nameTextStyle:{fontSize:11,color:theme.muted,align:"right",verticalAlign:"bottom"}});
});
it.each(["logs","traces","table","log_patterns"] as const)("%s notes follow the bounded scroll body",async viz=>{
  const h=await render({...p,viz},{...r,frame:{...r.frame!,truncated:true}});
  const body=h.querySelector<HTMLElement>('[data-panel-body]')!,notes=h.querySelector<HTMLElement>('[data-panel-notes]')!;
  expect(body.contains(notes)).toBe(false);expect(body.nextElementSibling).toBe(notes);
  expect(body.style.isolation).toBe("isolate");expect(body.style.flexBasis).toBe("0px");expect(body.style.overflow).toBe("auto");expect(notes.style.flexShrink).toBe("0");
});
