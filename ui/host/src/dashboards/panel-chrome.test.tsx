import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { within } from "@testing-library/dom";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { gaugeOption, chartThemeFor, timeseriesOption } from "../../../panels/compile";
import { analysisOption } from "../../../panels/analysis";
import { heatRamp } from "../../../panels/heat-scale";
import { withAnnotations } from "../../../panels/annotations";
import { relativeLuminance } from "../../../theme";
import { PanelCard } from "./panel-card";
import type { PanelDisplayResult } from "./panel-result";
import type { Panel, PanelResult } from "../../../panels/types";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({label,height}: {label:string;height:number|string}) => <div data-plot-budget role="img" aria-label={label} style={{height}}/> }));
const p:Panel={id:"p",title:"Panel",viz:"timeseries",query:{from:"spans"},drill:"traces"};
const r:PanelResult={id:"p",status:"ok",elapsed_ms:1,from_ms:0,to_ms:600000,interval:"1m",frame:{columns:[{name:"time",type:"time",role:"time"},{name:"calls",type:"number",role:"measure"}],values:[[0,60000],[1,2]],rows:2}};
const cleanup:(()=>void)[]=[];
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
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
  expect(o.yAxis.splitNumber).toBe(Math.max(3,Math.min(10,Math.round((height-o.grid.top-o.grid.bottom-22)/70)))-1);
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
  const fills=o.series[0].data.map((d:any)=>o.series[0].renderItem({}, {value:(i:number)=>d.value[i],coord:(v:number[])=>[v[0]/1000,20],size:()=>[60,20],visual:()=>"#fff"}).style.fill);
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

it.each(["ms","s","ns","count"])("uses nice capped ticks over 0–90 seconds (unit=%s)",unit=>{
 const factor=unit==="ms"?1000:unit==="ns"?1e9:1;
 const result={...r,frame:{...r.frame!,columns:[r.frame!.columns[0],{name:"duration",type:"number" as const,role:"measure" as const,unit}],values:[[0,60000],[0,90*factor]]}};
 const axis=(plotHeight:number)=>{const o=timeseriesOption(p,result,chartThemeFor(false),{width:1200,height:plotHeight+42}) as any;return o.yAxis;};
 const tall=axis(750),short=axis(200);
 expect(tall.interval).toBe(10*factor);expect((tall.max-tall.min)/tall.interval+1).toBeLessThanOrEqual(10);
 expect((short.max-short.min)/short.interval+1).toBe(3);expect(short.splitNumber).toBe(2);
});


it("keeps the chart body and plot budget identical after a transient refresh failure", async () => {
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanup.push(()=>root.unmount());
  const retry=vi.fn();
  const draw=async(result:PanelDisplayResult,staleAt?:number)=>act(async()=>root.render(<MantineProvider><PanelCard panel={p} title={p.title} result={result} staleAt={staleAt} loading={false} height={300} group="g" editing={false} agentAvailable={true} onRetry={retry}/></MantineProvider>));
  await draw(r);
  const body=host.querySelector<HTMLElement>('[data-panel-body]')!;
  const before=body.previousElementSibling;
  const titleGroup=host.querySelector('[data-panel-title]')!.parentElement!.parentElement!;
  const headerChildren=titleGroup.childElementCount;
  const plotHeight=host.querySelector<HTMLElement>('[data-plot-budget]')!.style.height;
  const style=body.getAttribute("style");
  await draw({...r,error:"Disconnected",request_error:[p.id]},Date.now()-60000);
  expect(body.previousElementSibling).toBe(before);
  expect(titleGroup.childElementCount).toBe(headerChildren);
  expect(body.getAttribute("style")).toBe(style);
  expect(host.querySelector<HTMLElement>('[data-plot-budget]')!.style.height).toBe(plotHeight);
  expect(plotHeight).toBe("212px");
  expect(host.querySelector('[data-panel-notes]')).toBeNull();
  const indicator=within(host).getByRole("button", {name: /Refresh failed: Disconnected/});
  expect(indicator).not.toBeNull();expect(body.contains(indicator)).toBe(false);
  const menu=within(host).getByRole("button", {name: /Panel menu/});
  await act(async()=>menu.click());
  const button=[...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(button=>button.textContent==="Retry")!;
  expect(button).toBeDefined();await act(async()=>button.click());expect(retry).toHaveBeenCalledOnce();
});


it.each([120,300])("bounds the full error state inside a %ipx body with inline Retry in short cards",async height=>{
  const observers:{callback:ResizeObserverCallback;elements:Element[]}[]=[];
  vi.stubGlobal("ResizeObserver",class { record:{callback:ResizeObserverCallback;elements:Element[]}; constructor(callback:ResizeObserverCallback){this.record={callback,elements:[]};observers.push(this.record);} observe(el:Element){this.record.elements.push(el);} disconnect(){} });
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanup.push(()=>root.unmount());
  const message="Server unavailable "+"very long details ".repeat(100),retry=vi.fn();
  await act(async()=>root.render(<MantineProvider><PanelCard panel={p} title={p.title} result={{id:p.id,status:"error",elapsed_ms:0,error:message,request_error:[p.id]}} loading={false} height={388} group="g" editing={false} agentAvailable={false} onRetry={retry}/></MantineProvider>));
  const body=host.querySelector<HTMLElement>('[data-panel-body]')!;
  const observer=observers.find(o=>o.elements.includes(body));
  expect(observer).toBeDefined();
  await act(async()=>observer!.callback([{target:body,contentRect:{height,width:300}} as unknown as ResizeObserverEntry],{} as ResizeObserver));
  const state=host.querySelector<HTMLElement>('[data-panel-error]')!;
  expect(state).not.toBeNull();expect(body.contains(state)).toBe(true);
  expect(state.style.height).toBe("100%");expect(state.style.maxHeight).toBe("100%");expect(state.style.minHeight).toBe("0");expect(state.style.overflow).toBe("hidden");
  expect(state.style.flexDirection).toBe(height<160?"row":"column");
  const text=state.querySelector<HTMLElement>('[data-panel-error-message]')!;
  expect(text.title).toBe(message);expect(text.style.minWidth).toBe("0");expect(text.style.overflow).toBe("hidden");
  if(height<160)expect(text.style.whiteSpace).toBe("nowrap");
  const button=[...state.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent==="Retry")!;
  expect(button.style.flexShrink).toBe("0");await act(async()=>button.click());expect(retry).toHaveBeenCalledOnce();
  vi.unstubAllGlobals();
});


it.each(["heatmap","state_timeline"] as const)("renders %s with literal styles and visual color without the deprecated api.style",viz=>{
  const theme=chartThemeFor(false);
  const frame={columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"service",type:"string" as const,role:"dimension" as const},{name:"calls",type:"number" as const,role:"measure" as const}],values:[[0],["cart"],[1]],rows:1};
  const option=analysisOption({...p,viz},{...r,frame},theme) as any;
  const datum=option.series[0].data[0];
  const visual=vi.fn((key:string)=>{expect(key).toBe("color");return theme.status.ok;});
  const rect=option.series[0].renderItem({}, {value:(i:number)=>datum.value[i],coord:(v:number[])=>[v[0]/1000,20],size:()=>[60,20],visual});
  expect(visual).toHaveBeenCalledExactlyOnceWith("color");
  expect(rect.style).toEqual({fill:viz==="heatmap"?heatRamp(theme)[0]:theme.status.ok,stroke:undefined,lineWidth:0});
  expect(rect.shape.height).toBe(viz==="heatmap"?19:13);
});

it("exposes the refresh failure on the menu button and opens its tooltip on keyboard focus", async () => {
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanup.push(() => root.unmount());
  await act(async () => root.render(<MantineProvider><PanelCard panel={p} title={p.title} result={{...r, error: "Disconnected"}} staleAt={Date.now() - 60000} loading={false} height={300} group="g" editing={false} agentAvailable={false}/></MantineProvider>));
  const menu = within(host).getByRole("button", {name: /Refresh failed: Disconnected/});
  expect(within(host).getByRole("button", {name: /Refresh failed/, description: /Refresh failed: Disconnected/})).toBe(menu);
  await act(async () => menu.focus());
  await vi.waitFor(() => expect(within(document.body).getByRole("tooltip").textContent).toContain("Refresh failed: Disconnected"), {timeout: 3000, interval: 5});
});

it("updates the failed frame's age in the menu name, description and focused tooltip", async () => {
  vi.useFakeTimers();
  try {
    const now = Date.parse("2026-10-08T12:00:00Z"); vi.setSystemTime(now);
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanup.push(() => root.unmount());
    await act(async () => root.render(<MantineProvider><PanelCard panel={p} title={p.title} result={{...r, error: "Disconnected"}} staleAt={now - 90_000} loading={false} height={300} group="g" editing={false} agentAvailable={false}/></MantineProvider>));
    const menu = within(host).getByRole("button", {name: /Refresh failed.*Showing data from 1 minute ago/});
    expect(within(host).getByRole("button", {name: /Refresh failed/, description: /Showing data from 1 minute ago/})).toBe(menu);
    await act(async () => menu.focus());
    await act(async () => vi.advanceTimersByTimeAsync(30_000));
    expect(within(host).getByRole("button", {name: /Refresh failed.*Showing data from 2 minutes ago/, description: /Showing data from 2 minutes ago/})).toBe(menu);
    expect(within(document.body).getByRole("tooltip").textContent).toContain("Showing data from 2 minutes ago");
    expect(host.querySelector('[data-panel-notes]')).toBeNull();
  } finally {vi.useRealTimers();}
});
