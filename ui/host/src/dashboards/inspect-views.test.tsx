import { afterEach, expect, it, vi } from "vitest";
import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { Panel, PanelResult } from "../../../panels/types";
import { PanelCard } from "./panel-card";
import { gaugeOption, chartThemeFor } from "../../../panels/compile";
import { analysisOption } from "../../../panels/analysis";
vi.mock("./echart-canvas",()=>({EChartCanvas:()=> <div/>}));
const cleanups:(()=>void)[]=[];
afterEach(async()=>{await act(async()=>cleanups.splice(0).forEach(fn=>fn()));vi.restoreAllMocks();document.body.innerHTML="";});
const result:PanelResult={id:"p",status:"ok",elapsed_ms:1,frame:{columns:[{name:"calls",type:"number",role:"measure"}],values:[[1]],rows:1}};
it.each([false,true])('Data exposes the full refresh error as safe text (stale=%s)',async stale=>{
  const message='Out of Memory: '+'details '.repeat(120)+'<b id="injected">final diagnostic</b>';
  const h=await render({id:'p',title:'Requests',viz:'table'},
    {...result,status:'error',frame:stale?result.frame:undefined,error:message});
  const button=h.querySelector<HTMLButtonElement>('[data-panel-view="Data"]');
  expect(button).not.toBeNull();
  await act(async()=>button!.click());
  const detail=h.querySelector<HTMLElement>('[data-panel-error-detail]');
  expect(detail?.textContent).toBe(message);
  expect(detail?.style.userSelect).toBe('text');
  expect(detail?.style.whiteSpace).toBe('pre-wrap');
  expect(detail?.style.overflowWrap).toBe('anywhere');
  expect(h.querySelector('#injected')).toBeNull();
  expect(h.querySelector('[data-panel-data] tbody tr')!==null).toBe(stale);
});
async function render(panel:Panel, r:PanelResult=result, onSelect?: (value:string)=>void, height=300) {
 vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);
 const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
 await act(async()=>root.render(<MantineProvider><PanelCard panel={panel} title={panel.title} result={r} loading={false} height={height} group="g" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} onSelect={onSelect}/></MantineProvider>));
 return host;
}

it.each([false,true])('short narrow error cards expose Data through keyboard menu navigation (stale=%s)',async stale=>{
  const observer=globalThis.ResizeObserver;
  vi.stubGlobal('ResizeObserver',class {
    constructor(private callback:ResizeObserverCallback){}
    observe(target:Element){this.callback([{target,contentRect:{width:300,height:92}} as ResizeObserverEntry],this as unknown as ResizeObserver);}
    disconnect(){} unobserve(){}
  });
  try {
    const message='Out of Memory: '+'details '.repeat(120)+'final diagnostic';
    const h=await render({id:'p',title:'Requests',viz:'table'},
      {...result,status:'error',frame:stale?result.frame:undefined,error:message},undefined,180);
    expect(h.querySelector('[data-compact-views="true"]')).not.toBeNull();
    expect(h.querySelector('[data-panel-view="Data"]')).toBeNull();
    const menu=h.querySelector<HTMLButtonElement>('[aria-label^="Requests menu"]')!;
    // DOM key dispatch does not synthesize native button activation.
    await act(async()=>{menu.focus();menu.click();});
    await vi.waitFor(()=>expect(document.querySelector('[role="menuitemradio"][data-panel-view="Chart"]')).not.toBeNull());
    await act(async()=>document.querySelector<HTMLElement>('[role="menuitemradio"][data-panel-view="Chart"]')!.focus());
    await act(async()=>document.activeElement!.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowDown',bubbles:true,cancelable:true})));
    expect(document.activeElement?.getAttribute('data-panel-view')).toBe('Data');
    await act(async()=>(document.activeElement as HTMLElement).click());
    expect(h.querySelector('[data-panel-error-detail]')?.textContent).toBe(message);
    expect(h.querySelector('[data-panel-data] tbody tr')!==null).toBe(stale);
    expect(h.querySelector<HTMLElement>('[data-panel-body]')?.style.overflow).toBe('auto');
  } finally {vi.stubGlobal('ResizeObserver',observer);}
});

it.each([{width:219,height:180},{width:300,height:139},{width:238,height:56}])("renders a meter in short and narrow bodies (%j)", size => {
  const option = gaugeOption({id:"g",title:"Gauge",viz:"gauge",min:0,max:10,thresholds:[{value:1,status:"warn"}]},1.76,chartThemeFor(false),"percent",size) as any;
  expect(option.series).toEqual([]);
  expect(option.graphic.find((g:any)=>g.id==="gauge-value").style).toMatchObject({text:"1.76%",fontSize:28});
  expect(option.graphic.find((g:any)=>g.id==="gauge-track").shape.height).toBe(10);
  expect(option.graphic.filter((g:any)=>g.type==="text").map((g:any)=>g.style.text)).toEqual(expect.arrayContaining(["0.00%","10.0%","■ Warn"]));
});
it.each([
 {viz:"timeseries",drill:"logs",hint:"click for matching logs"},
 {viz:"timeseries",drill:"traces",hint:"click for exemplar traces"},
 {viz:"heatmap",drill:"traces",hint:"click for exemplar traces"},
 {viz:"bar",click:{set_variable:"service"},hint:"click to filter by service"},
 {viz:"scatter",click:{set_variable:"operation"},hint:"click to filter by operation"},
 {viz:"table",drill:"traces",hint:"click to open trace"},
 {viz:"traces",hint:"click to open trace"},
] as const)("subtitle maps the actual interaction (%j)",async sample=>{
 const {hint,...settings}=sample;
 const h=await render({id:"p",title:"Panel",...settings});
 expect(h.querySelector('[data-panel-subtitle]')?.textContent).toContain(hint);
});
it.each(["scatter","histogram"] as const)("%s reserves grid space for titles outside the data",viz=>{
 const frame={columns:[{name:"operation",type:"string" as const,role:"dimension" as const},{name:"calls",type:"number" as const,role:"measure" as const},{name:"p95",type:"number" as const,role:"measure" as const}],values:[["GET"],[100],[50]],rows:1};
 const o=analysisOption({id:"p",title:"Panel",viz},{...result,frame},chartThemeFor(false),{width:500,height:210}) as any;
 expect(o.yAxis.nameGap).toBeGreaterThanOrEqual(8);expect(o.yAxis.nameTextStyle.verticalAlign).toBe("bottom");expect(o.grid.top).toBeGreaterThanOrEqual(28);
 if(viz==="scatter"){expect(o.xAxis.nameLocation).toBe("middle");expect(o.xAxis.nameGap).toBeGreaterThanOrEqual(28);expect(o.grid.bottom).toBeGreaterThanOrEqual(32);}
});
it.each([false,true])("health rows have fixed icon/name/value columns and explicit alignment (interactive=%s)",async interactive=>{
 const frame={columns:[{name:"service",type:"string" as const,role:"dimension" as const},{name:"health",type:"string" as const,role:"dimension" as const},{name:"error_rate",type:"number" as const,role:"measure" as const}],values:[["cart"],["unhealthy"],[7]],rows:1,health:{health:"unhealthy" as const,service_count:1,total_spans:100,error_rate:7,error_trend:[],counts:{healthy:0,degraded:0,unhealthy:1}}};
 const h=await render({id:"p",title:"Health",viz:"health"},{...result,frame},interactive?vi.fn():undefined);
 const row=h.querySelector<HTMLElement>('[data-health-service]')!;
 expect(row.style.display).toBe("grid");expect(row.style.gridTemplateColumns).toBe("12px minmax(0, 1fr) 64px");expect(row.style.textAlign).toBe("left");
 expect((row.children[1] as HTMLElement).style.textAlign).toBe("left");expect((row.children[2] as HTMLElement).style.textAlign).toBe("right");
});
it.each([false,true])("table body shows a 16px fade only for measured scrolling (%s)",async overflow=>{
 vi.spyOn(HTMLElement.prototype,"scrollHeight","get").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-panel-body")&&overflow?300:100;});
 vi.spyOn(HTMLElement.prototype,"clientHeight","get").mockReturnValue(100);
 const h=await render({id:"p",title:"Patterns",viz:"log_patterns"},{...result,frame:{...result.frame!,truncated:true}});
 const fade=h.querySelector<HTMLElement>('[data-panel-scroll-fade]');
 if(overflow){expect(fade).not.toBeNull();expect(fade!.style.height).toBe("16px");expect(fade!.style.pointerEvents).toBe("none");}else expect(fade).toBeNull();
 const body=h.querySelector('[data-panel-body]')!,note=h.querySelector('[data-panel-notes]')!;
 expect(body.contains(note)).toBe(false);expect(body.nextElementSibling).toBe(note);
});
