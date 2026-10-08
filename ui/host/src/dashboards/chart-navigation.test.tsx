import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import * as extraction from "../../../panels/keyboard";
import { EChartCanvas } from "./echart-canvas";
import { PanelFullscreen } from "./panel-fullscreen";
import { ShortcutsHelp } from "./shortcuts-help";
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
afterEach(async()=>{await act(async()=>cleanups.splice(0).forEach(fn=>fn()));document.body.innerHTML="";vi.restoreAllMocks();vi.unstubAllGlobals();chart.on.mockClear();});
async function key(target:HTMLElement,key:string,extra:KeyboardEventInit={}) {
  await act(async()=>target.focus());
  await act(async()=>target.dispatchEvent(new KeyboardEvent("keydown",{key,bubbles:true,cancelable:true,...extra})));
}
async function mount(next=option,fullscreen=false) {
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);cleanups.push(()=>root.unmount());
  const click=vi.fn(),zoom=vi.fn(),close=vi.fn();
  const p:Panel={id:"p",title:"Requests",viz:"timeseries",query:{from:"spans",by:["service"]}};
  const draw=async(compiled:typeof option,bounds={from:0,to:5000})=>act(async()=>root.render(<MantineProvider>
    {fullscreen?<PanelFullscreen opened title="Requests" onClose={close} returnFocusTo={()=>undefined}>
      <EChartCanvas option={compiled} height={200} label="Requests" onClick={click} onZoom={zoom} keyboard={{bounds,canSelect:e=>Boolean(pointSelection(p,{id:"p",status:"ok",elapsed_ms:0},e))}}/>
    </PanelFullscreen>:<EChartCanvas option={compiled} height={200} label="Requests" onClick={click} onZoom={zoom} keyboard={{bounds,canSelect:e=>Boolean(pointSelection(p,{id:"p",status:"ok",elapsed_ms:0},e))}}/>}
  </MantineProvider>));
  await draw(next);
  const surface=()=>fullscreen?document.querySelector<HTMLElement>('[role="dialog"]')!:el;
  const focus=async()=>{
    const explore=[...surface().querySelectorAll<HTMLButtonElement>("button")].find(b=>b.textContent==="Explore chart");
    if(explore)await act(async()=>explore.click());
    const plot=surface().querySelector<HTMLElement>("[data-chart-plot],[data-chart-point]")??surface().querySelector<HTMLElement>('[role="img"]')!;
    await act(async()=>plot.focus());return plot;
  };
  return {el,surface,draw,focus,click,zoom,close};
}
it("makes the plot the only chart tab stop and shows its exact hint only on focus",async()=>{
  const view=await mount();
  expect(view.el.textContent).not.toContain("Explore chart");
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
  expect([...view.surface().querySelectorAll<HTMLInputElement>("input")].filter(i=>i.type!=="number").map(i=>i.value))
    .toEqual(["1970-01-01T00:00:01.000Z","1970-01-01T00:00:04.000Z"]);
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
  expect(view.surface().querySelector('[aria-live]')?.textContent).toContain("Other");
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
it("offers a single-key toggle and omits host-composer claims from fragment help",async()=>{
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);cleanups.push(()=>root.unmount());
  await act(async()=>root.render(<MantineProvider><ShortcutsHelp opened dashboard={false} onClose={vi.fn()}/></MantineProvider>));
  expect(document.body.textContent).not.toContain("global /");
  expect(document.body.textContent).toContain("Single-key shortcuts");
  expect(document.body.textContent).not.toContain("Explore chart");
});
