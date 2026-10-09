import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import * as extraction from "../../../panels/keyboard";
import { EChartCanvas } from "./echart-canvas";
import { AnalysisChart } from "./viz/analysis-chart";
import { PanelCard } from "./panel-card";
import { PanelFullscreen } from "./panel-fullscreen";
import { pointSelection } from "../../../panels/interaction";
import type { Panel } from "../../../panels/types";

const chart = vi.hoisted(() => ({setOption:vi.fn(),dispatchAction:vi.fn(),dispose:vi.fn(),on:vi.fn(),resize:vi.fn()}));
vi.mock("echarts/core",()=>({init:()=>chart,use:()=>{},connect:()=>{},disconnect:()=>{}}));
vi.mock("echarts/charts",()=>({BarChart:{},LineChart:{},CustomChart:{},HeatmapChart:{},ScatterChart:{}}));
vi.mock("echarts/components",()=>({AriaComponent:{},BrushComponent:{},DataZoomComponent:{},GraphicComponent:{},GridComponent:{},LegendComponent:{},MarkAreaComponent:{},MarkLineComponent:{},ToolboxComponent:{},TooltipComponent:{},VisualMapComponent:{}}));
vi.mock("echarts/features",()=>({LabelLayout:{}}));
vi.mock("echarts/renderers",()=>({CanvasRenderer:{}}));
const option={xAxis:{type:"time"},series:[{name:"checkout",type:"line",interactive:true,data:[[1000,1],[2000,2],[3000,3],[4000,4]]}]};
const cleanups:(()=>void)[]=[];
afterEach(async()=>{await act(async()=>cleanups.splice(0).forEach(fn=>fn()));document.body.innerHTML="";vi.restoreAllMocks();vi.unstubAllGlobals();vi.useRealTimers();chart.on.mockClear();chart.dispatchAction.mockClear();chart.setOption.mockClear();});
async function key(target:HTMLElement,key:string,extra:KeyboardEventInit={}) {
  await act(async()=>target.focus());
  await act(async()=>target.dispatchEvent(new KeyboardEvent("keydown",{key,bubbles:true,cancelable:true,...extra})));
}
async function mount(next=option,fullscreen=false) {
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);cleanups.push(()=>root.unmount());
  const click=vi.fn(),zoom=vi.fn(),close=vi.fn(),pending=vi.fn();
  const p:Panel={id:"p",title:"Requests",viz:"timeseries",query:{from:"spans",by:["service"]}};
  const draw=async(compiled:typeof option,bounds={from:0,to:5000})=>act(async()=>root.render(<MantineProvider>
    {fullscreen?<PanelFullscreen opened title="Requests" onClose={close} returnFocusTo={()=>undefined}>
      <EChartCanvas option={compiled} height={200} label="Requests" onClick={click} onZoom={zoom} keyboard={{bounds,onRangePending:pending,canSelect:e=>Boolean(pointSelection(p,{id:"p",status:"ok",elapsed_ms:0},e))}}/>
    </PanelFullscreen>:<EChartCanvas option={compiled} height={200} label="Requests" onClick={click} onZoom={zoom} keyboard={{bounds,onRangePending:pending,canSelect:e=>Boolean(pointSelection(p,{id:"p",status:"ok",elapsed_ms:0},e))}}/>}
  </MantineProvider>));
  await draw(next);
  const surface=()=>fullscreen?document.querySelector<HTMLElement>('[role="dialog"]')!:el;
  const focus=async()=>{
    const plot=surface().querySelector<HTMLElement>("[data-chart-plot]")!;
    await act(async()=>plot.focus());return plot;
  };
  return {el,surface,draw,focus,click,zoom,close,pending};
}
it("makes the plot the only chart tab stop and shows its exact hint only on focus",async()=>{
  const view=await mount();
  const plot=await view.focus();expect(plot.tabIndex).toBe(0);
  expect(view.el.querySelectorAll('[tabindex="0"]')).toHaveLength(1);
  expect(plot.getAttribute("aria-label")).toContain("1 series");
  expect(view.el.textContent).toContain("←→ points · ↑↓ series · Enter drill · Shift+←→ range");
  expect(view.el.textContent).not.toContain("Zoom to range");
});
it("allows repeated arrows, Home/End, and exactly one Enter action",async()=>{
  const view=await mount(),plot=await view.focus();
  await key(plot,"ArrowRight",{repeat:true});await key(plot,"Enter");
  expect(view.click.mock.lastCall?.[0].value).toEqual([2000,2]);
  view.click.mockClear();await key(plot,"Enter",{repeat:true});expect(view.click).not.toHaveBeenCalled();
  await key(plot,"End");await key(plot,"Enter");expect(view.click.mock.lastCall?.[0].value).toEqual([4000,4]);
  await key(plot,"Home");await key(plot,"Enter");expect(view.click.mock.lastCall?.[0].value).toEqual([1000,1]);
});
it("retains point and range identities through a refresh and resize between presses",async()=>{
  let resize!:ResizeObserverCallback;
  vi.stubGlobal("ResizeObserver",class{constructor(callback:ResizeObserverCallback){resize=callback;}observe(){}disconnect(){}});
  const view=await mount(),plot=await view.focus();
  await key(plot,"ArrowRight",{shiftKey:true});
  await act(async()=>resize([],{} as ResizeObserver));
  const shifted={...option,series:[{...option.series[0],data:[[0,0],...option.series[0].data]}]};
  await view.draw(shifted,{from:500,to:6000});
  expect(view.surface().querySelector('[aria-live]')?.textContent).toContain("00:00:02.000Z");
  await key(plot,"ArrowRight",{shiftKey:true});
  expect(view.surface().querySelector<HTMLElement>('[data-chart-range-label]')?.title).toBe("1970-01-01T00:00:01.000Z – 1970-01-01T00:00:04.000Z");
});
it("matches a timestamp when changing series with gaps",async()=>{
  const view=await mount({...option,series:[...option.series,{name:"cart",type:"line",interactive:true,data:[[1000,8],[3000,9]]}]}),plot=await view.focus();
  await key(plot,"ArrowRight");await key(plot,"ArrowRight");await key(plot,"ArrowDown");await key(plot,"Enter");
  expect(view.click.mock.lastCall?.[0].seriesName).toBe("cart");expect(view.click.mock.lastCall?.[0].value).toEqual([3000,9]);
  await key(plot,"ArrowUp");await key(plot,"Enter");expect(view.click.mock.lastCall?.[0].value).toEqual([3000,3]);
  await key(plot,"ArrowLeft");await key(plot,"ArrowDown");await key(plot,"Enter");
  expect(view.click.mock.lastCall?.[0].value).toEqual([1000,8]); // nearest timestamp, stable tie
});
it("reads previous and Other points without permitting selection",async()=>{
  const view=await mount({...option,series:[{...option.series[0],name:"Other"},{...option.series[0],name:"checkout · previous",interactive:false}]}),plot=await view.focus();
  await key(plot,"Home");expect(view.surface().querySelector('[aria-live]')?.textContent).toContain("Other");
  await key(plot,"Enter");expect(view.click).not.toHaveBeenCalled();
  await key(plot,"ArrowDown");expect(view.surface().querySelector('[aria-live]')?.textContent).toContain("checkout · previous");
  await key(plot,"Enter");expect(view.click).not.toHaveBeenCalled();
});
it("includes the complete final selected bucket when zooming",async()=>{
  const view=await mount(),plot=await view.focus();await key(plot,"ArrowRight",{shiftKey:true});
  const zoom=[...view.el.querySelectorAll<HTMLButtonElement>("button")].find(b=>b.textContent==="Zoom to range")!;
  await act(async()=>zoom.click());expect(view.zoom).toHaveBeenCalledExactlyOnceWith(1000,3000);
  expect(view.el.textContent).not.toContain("Zoom to range");
});
it("cancels the range before Escape can close full-screen, then permits the next Escape",async()=>{
  const view=await mount(option,true),plot=await view.focus();await key(plot,"ArrowRight",{shiftKey:true});
  expect(view.surface().textContent).toContain("Zoom to range");
  await key(plot,"Escape");expect(view.close).not.toHaveBeenCalled();expect(view.surface().textContent).not.toContain("Zoom to range");
  await key(plot,"Escape");expect(view.close).toHaveBeenCalledOnce();
});
it("does not extract candidates until the plot is focused",async()=>{
  const spy=vi.spyOn(extraction,"keyboardPoints"),view=await mount();
  expect(spy).not.toHaveBeenCalled();await view.focus();expect(spy).toHaveBeenCalled();
});

it("keeps the plot height identical when unfocused, focused and range-pending",async()=>{
  const view=await mount(),plot=view.el.querySelector<HTMLElement>('[data-chart-plot]')!,canvas=plot.querySelector<HTMLElement>('[role="img"]')!;
  // happy-dom has no layout engine: nonzero computed heights plus absolute
  // descendants verify the size contract rather than comparing zero DOMRects.
  const heights=[Number.parseFloat(getComputedStyle(plot).height)];
  await view.focus();heights.push(Number.parseFloat(getComputedStyle(plot).height));
  await key(plot,'ArrowRight',{shiftKey:true});heights.push(Number.parseFloat(getComputedStyle(plot).height));
  expect(heights).toEqual([200,200,200]);
  expect(getComputedStyle(canvas).position).toBe('absolute');expect(getComputedStyle(canvas).height).toBe('100%');
  const overlay=plot.querySelector<HTMLElement>('[data-chart-overlay]')!;
  expect(overlay).not.toBeNull();expect(getComputedStyle(overlay).position).toBe('absolute');
  for(const element of overlay.querySelectorAll<HTMLElement>('[data-chart-hint],[data-chart-readout],[data-chart-range-bar]'))expect(getComputedStyle(element).position).toBe('absolute');
  expect(overlay.querySelector('[data-chart-hint]')).toBeNull();expect(overlay.querySelector('[data-chart-readout]')).toBeNull();expect(overlay.querySelector('[data-chart-range-bar]')).not.toBeNull();
});
it("draws and updates the pending range with the native mouse brush band without zooming",async()=>{
  const view=await mount(),plot=await view.focus();
  const brush=chart.setOption.mock.calls.find(([option])=>option.brush)?.[0].brush;
  expect(brush).toMatchObject({xAxisIndex:0,brushMode:'single'});
  const areas=()=>chart.dispatchAction.mock.calls.filter(([action])=>action.type==='brush').at(-1)?.[0].areas;
  await key(plot,'ArrowRight',{shiftKey:true});expect(areas()).toEqual([{brushType:'lineX',xAxisIndex:0,coordRange:[1000,3000]}]);
  expect(chart.dispatchAction).toHaveBeenLastCalledWith({type:'brush',areas:areas()},{silent:true});
  await key(plot,'ArrowRight',{shiftKey:true});expect(areas()[0].coordRange).toEqual([1000,4000]);
  await key(plot,'ArrowLeft',{shiftKey:true});expect(areas()[0].coordRange).toEqual([1000,3000]);expect(view.zoom).not.toHaveBeenCalled();
  await view.draw({...option},{from:0,to:5000});expect(areas()[0].coordRange).toEqual([1000,3000]);
  await key(plot,'Escape');expect(areas()).toEqual([]);expect(view.zoom).not.toHaveBeenCalled();
  await key(plot,'ArrowLeft',{shiftKey:true});
  await act(async()=>[...view.el.querySelectorAll<HTMLButtonElement>('button')].find(b=>b.textContent==='Zoom to range')!.click());
  expect(view.zoom).toHaveBeenCalledOnce();expect(areas()).toEqual([]);
});
it("clears the keyboard preview when a mouse brush commits through the same zoom callback",async()=>{
  const view=await mount(),plot=await view.focus();await key(plot,'ArrowRight',{shiftKey:true});
  const end=chart.on.mock.calls.find(([name])=>name==='brushEnd')![1];
  await act(async()=>end({areas:[{coordRange:[1000,4000]}]}));
  expect(view.zoom).toHaveBeenCalledExactlyOnceWith(1000,4000);expect(view.el.textContent).not.toContain('Zoom to range');
});
it.each([['2026-10-08','18:46:00 – 18:48:30 UTC · 2m 30s'],['2026-10-07','2026-10-07 18:46:00 – 18:48:30 UTC · 2m 30s']])("shows a compact range label with full ISO in its title for %s",async(date,label)=>{
  vi.useFakeTimers({toFake:['Date']});vi.setSystemTime(new Date('2026-10-08T20:00:00Z'));
  const from=Date.parse(date+'T18:46:00Z'),next={...option,series:[{...option.series[0],data:[[from,1],[from+30000,2],[from+150000,3],[from+180000,4]]}]};
  const view=await mount(next);await view.draw(next,{from,to:from+210000});const plot=await view.focus();await key(plot,'ArrowRight',{shiftKey:true});
  expect(view.el.querySelectorAll('input')).toHaveLength(0);
  const range=view.el.querySelector<HTMLElement>('[data-chart-range-label]')!;
  expect(range?.textContent).toBe(label);expect(range.title).toBe(new Date(from).toISOString()+' – '+new Date(from+150000).toISOString());
  expect(view.el.textContent).not.toContain('Paused while selecting');
  expect([...view.el.querySelectorAll('button')].map(b=>b.textContent)).toEqual(['Zoom to range','Cancel']);
});

it("replaces the subtitle interaction hint without covering the legend and uses only the native point tooltip",async()=>{
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);cleanups.push(()=>root.unmount());
  const panel:Panel={id:"p",title:"Requests",viz:"timeseries",query:{from:"spans"},drill:"traces"};
  const result={id:"p",status:"ok" as const,elapsed_ms:0,from_ms:1000,to_ms:5000,interval:"1s",frame:{columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"count",type:"number" as const,role:"measure" as const}],values:[[1000,2000],[1,2]],rows:2}};
  await act(async()=>root.render(<MantineProvider><PanelCard panel={panel} title="Requests" result={result} loading={false} height={288} group="g" editing={false} agentAvailable={false} onZoom={vi.fn()}/></MantineProvider>));
  const subtitle=el.querySelector<HTMLElement>('[data-panel-subtitle]')!;
  expect(subtitle.textContent).toContain('click for exemplar traces');
  const plot=el.querySelector<HTMLElement>('[data-chart-plot]')!;await act(async()=>plot.focus());
  const hint=el.querySelector<HTMLElement>('[data-chart-hint]')!;
  expect(subtitle.contains(hint)).toBe(true);expect(hint.textContent).toBe('←→ points · ↑↓ series · ↵ drill · ⇧←→ range');
  expect(document.getElementById(plot.getAttribute('aria-describedby')!)?.textContent).toBe('←→ points · ↑↓ series · Enter drill · Shift+←→ range');
  expect(plot.querySelector('[data-chart-hint]')).toBeNull();expect(plot.querySelector('[data-chart-readout]')).toBeNull();
  await key(plot,'ArrowRight');expect(chart.dispatchAction).toHaveBeenCalledWith({type:'showTip',seriesIndex:0,dataIndex:1});
  expect(plot.querySelector('[aria-live]')?.textContent).toContain('1970-01-01T00:00:02.000Z');
  await act(async()=>plot.blur());expect(subtitle.textContent).toContain('click for exemplar traces');expect(el.querySelector('[data-chart-hint]')).toBeNull();
});
it("reserves the measured range bar height below axis labels only while pending, including wrapping and resize",async()=>{
  const observers:{callback:ResizeObserverCallback;elements:Element[]}[]=[];
  vi.stubGlobal('ResizeObserver',class{record:{callback:ResizeObserverCallback;elements:Element[]};constructor(callback:ResizeObserverCallback){this.record={callback,elements:[]};observers.push(this.record);}observe(el:Element){this.record.elements.push(el);}disconnect(){}});
  vi.spyOn(HTMLElement.prototype,'getBoundingClientRect').mockImplementation(function(this:HTMLElement){return {height:this.hasAttribute('data-chart-range-bar')?28:200,width:500,x:0,y:0,top:0,left:0,bottom:200,right:500,toJSON(){}};});
  const next={...option,grid:{top:28,bottom:8,containLabel:true}};
  const view=await mount(next),plot=await view.focus();
  const grid=()=>chart.setOption.mock.calls.filter(([option])=>option.grid).at(-1)?.[0].grid;
  expect(grid().bottom).toBe(8);await key(plot,'ArrowRight',{shiftKey:true});
  const bar=view.el.querySelector<HTMLElement>('[data-chart-range-bar]')!;
  expect(grid().bottom).toBe(36);expect(plot.style.height).toBe('200px');
  const observer=observers.find(o=>o.elements.includes(bar))!;expect(observer).toBeDefined();
  await act(async()=>observer.callback([{target:bar,contentRect:{height:56},borderBoxSize:[{blockSize:56}]} as unknown as ResizeObserverEntry],{} as ResizeObserver));
  expect(grid().bottom).toBe(64);
  await key(plot,'Escape');expect(grid().bottom).toBe(8);expect(plot.style.height).toBe('200px');
});


it.each(["bar", "histogram", "scatter", "timeseries", "heatmap", "state_timeline"] as const)("keeps pointer focus out of keyboard mode for %s until navigation",async viz=>{
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);cleanups.push(()=>root.unmount());
  const panel:Panel={id:"p",title:"Requests",viz,query:{from:"spans",by:["service"]},click:{set_variable:"service"}};
  const result={id:"p",status:"ok" as const,elapsed_ms:0,from_ms:1000,to_ms:5000,interval:"1s",frame:{columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"service",type:"string" as const,role:"dimension" as const},{name:"count",type:"number" as const,role:"measure" as const},{name:"duration",type:"number" as const,role:"measure" as const}],values:[[1000,2000],["cart","cart"],[1,2],[3,4]],rows:2}};
  const draw=()=>act(async()=>root.render(<MantineProvider><PanelCard panel={panel} title="Requests" result={{...result}} loading={false} height={288} group="g" editing={false} agentAvailable={false} onZoom={vi.fn()}/></MantineProvider>));
  await draw();const plot=el.querySelector<HTMLElement>('[data-chart-plot]')!;
  expect(document.getElementById(plot.getAttribute("aria-describedby")!)).not.toBeNull();
  const spy=vi.spyOn(extraction,"keyboardPoints");chart.dispatchAction.mockClear();
  await act(async()=>{plot.dispatchEvent(new Event("pointerdown",{bubbles:true}));plot.focus();plot.click();});
  await draw();
  expect(spy).not.toHaveBeenCalled();
  expect(chart.dispatchAction.mock.calls.some(([action])=>["highlight","showTip"].includes(action.type))).toBe(false);
  expect(el.querySelector('[data-chart-hint]')).toBeNull();
  await key(plot,"ArrowRight");
  expect(spy).toHaveBeenCalled();expect(el.querySelector('[data-chart-hint]')).not.toBeNull();
  expect(chart.dispatchAction.mock.calls.some(([action])=>action.type==="showTip")).toBe(true);
});


it("retains the bottom state timeline row and pending range when reserving range bar height",async()=>{
  vi.spyOn(HTMLElement.prototype,"clientWidth","get").mockReturnValue(500);
  vi.spyOn(HTMLElement.prototype,"clientHeight","get").mockReturnValue(200);
  vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){return {height:this.hasAttribute("data-chart-range-bar")?28:200,width:500,x:0,y:0,top:0,left:0,bottom:200,right:500,toJSON(){}};});
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);cleanups.push(()=>root.unmount());
  const panel:Panel={id:"p",title:"States",viz:"state_timeline",query:{from:"spans",by:["service"]}};
  const rows=Array.from({length:9},(_,i)=>[`service${i}`,`service${i}`]).flat();
  const result={id:"p",status:"ok" as const,elapsed_ms:0,from_ms:1000,to_ms:5000,interval:"1s",frame:{columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"service",type:"string" as const,role:"dimension" as const},{name:"count",type:"number" as const,role:"measure" as const}],values:[rows.map((_,i)=>i%2?3000:1000),rows,rows.map(()=>1)],rows:18}};
  const point=vi.fn();await act(async()=>root.render(<MantineProvider><AnalysisChart panel={panel} result={result} dark={false} height={200} onZoom={vi.fn()} onPoint={point}/></MantineProvider>));
  const plot=el.querySelector<HTMLElement>('[data-chart-plot]')!;
  await key(plot,"End");
  await key(plot,"ArrowLeft",{shiftKey:true});
  expect(el.querySelector('[data-chart-range-label]')).not.toBeNull();
  expect(chart.setOption.mock.calls.filter(([option])=>option.yAxis).at(-1)![0].yAxis.data).toHaveLength(9);
  await key(plot,"Enter");expect(point.mock.lastCall?.[0].dimensions).toEqual({service:"service8"});
});
it("keeps the selected identity and range across a temporarily missing bucket",async()=>{
  const view=await mount(),plot=await view.focus();await key(plot,"End");await key(plot,"ArrowLeft",{shiftKey:true});
  const sparse={...option,series:[{...option.series[0],data:[[1000,1],[2000,2],[3000,null],[4000,null]]}]};
  await view.draw(sparse as unknown as typeof option);
  expect(view.el.querySelector('[data-chart-range-label]')).not.toBeNull();
  await view.draw(option);await key(plot,"Enter");expect(view.click.mock.lastCall?.[0].value).toEqual([3000,3]);
});


it("announces only navigation keys and stays silent on focus, refresh and recompute",async()=>{
  const view=await mount();const live=()=>view.el.querySelector('[aria-live]')!.textContent;
  const plot=await view.focus();expect(live()).toBe("");
  await key(plot,"End");const spoken=live();expect(spoken).toContain("00:00:04.000Z");
  await view.draw({...option,series:[{...option.series[0],data:[[1000,11],[2000,22],[3000,33],[4000,44]]}]},{from:0,to:6000});
  expect(live()).toBe(spoken);
  await key(plot,"ArrowLeft");expect(live()).toContain("33");expect(live()).not.toBe(spoken);
});


it("describes the application chart before focus while keeping the visible hint focus-only",async()=>{
  const view=await mount(),plot=view.el.querySelector<HTMLElement>('[data-chart-plot]')!;
  expect(plot.getAttribute("role")).toBe("application");expect(plot.getAttribute("aria-roledescription")).toBe("chart");
  expect(plot.getAttribute("aria-label")).toContain("Requests: 1 series");
  const description=document.getElementById(plot.getAttribute("aria-describedby")!);
  expect(description).not.toBeNull();expect(description!.textContent).toContain("Shift+←→ range");
  expect(view.el.querySelector('[data-chart-hint]')).toBeNull();
});


it("cancels a range when focus leaves the plot and resumes refresh, but retains it in range buttons",async()=>{
  const view=await mount(),plot=await view.focus();await key(plot,"ArrowRight",{shiftKey:true});
  expect(view.pending).toHaveBeenLastCalledWith(true);
  const zoom=[...view.el.querySelectorAll<HTMLButtonElement>("button")].find(button=>button.textContent==="Zoom to range")!;
  await act(async()=>zoom.focus());expect(view.el.querySelector('[data-chart-range-label]')).not.toBeNull();
  expect(view.pending).toHaveBeenLastCalledWith(true);
  const outside=document.createElement("button");document.body.append(outside);
  await act(async()=>outside.focus());expect(view.el.querySelector('[data-chart-range-label]')).toBeNull();
  expect(view.pending).toHaveBeenLastCalledWith(false);
});
