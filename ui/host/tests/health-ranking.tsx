import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { gaugeOption, chartThemeFor, timeseriesOption } from "../../panels/compile";
import { analysisOption } from "../../panels/analysis";
import { heatRamp } from "../../panels/heat-scale";
import { withAnnotations } from "../../panels/annotations";
import { worstHealthServices } from "../../panels/rollups";
import { relativeLuminance } from "../../theme";
import { PanelCard } from "../src/dashboards/panel-card";
import type { Panel, PanelResult } from "../../panels/types";

vi.mock("../src/dashboards/echart-canvas", () => ({ EChartCanvas: ({label}: {label:string}) => <div role="img" aria-label={label}/> }));
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

it("health lists four services in Wilson confidence order, excluding unsupported latency, with service links",async()=>{
  const names=["single","twenty","large","steady","latency","unknown"],frame={columns:[{name:"service",type:"string" as const,role:"dimension" as const},{name:"health",type:"string" as const,role:"dimension" as const},{name:"spans",type:"number" as const,role:"measure" as const},{name:"error_rate",type:"number" as const,role:"measure" as const},{name:"p95_ms",type:"number" as const,role:"measure" as const}],values:[names,names.map(()=>"unhealthy"),[1,20,2000,500,10,0],[100,50,40,5,0,null],[null,null,null,null,99999,null]],rows:6,health:{health:"unhealthy",counts:{healthy:0,degraded:0,unhealthy:6},service_count:6,total_spans:2531,error_rate:34,error_trend:[]}};
  const select=vi.fn(),h=await render({...p,viz:"health",drill:undefined},{...r,frame},select);
  const rows=[...h.querySelectorAll<HTMLElement>('[data-health-service]')];expect(rows.map(n=>n.dataset.healthService)).toEqual(["large","twenty","steady","latency"]);
  expect(rows.every(n=>n.querySelector("svg"))).toBe(true);expect(rows[0].textContent).toContain("40.0%");
  await act(async()=>rows[0].click());expect(select).toHaveBeenCalledWith("large");
});

it("uses the service variable when the health panel has no explicit click binding",async()=>{
 const frame={columns:[{name:"service",type:"string" as const,role:"dimension" as const},{name:"spans",type:"number" as const,role:"measure" as const},{name:"error_rate",type:"number" as const,role:"measure" as const}],values:[["cart"],[200],[5]],rows:1,health:{health:"unhealthy",counts:{healthy:0,degraded:0,unhealthy:1},service_count:1,total_spans:200,error_rate:5,error_trend:[]}};
 const variable=vi.fn(),h=await render({...p,viz:"health"},{...r,frame},undefined,{service:""},variable);
 await act(async()=>h.querySelector<HTMLElement>("[data-health-service]")!.click());expect(variable).toHaveBeenCalledWith("service","cart");
});

it("Wilson bounds are nonnegative for an eligible service with zero errors",()=>{
 const [service]=worstHealthServices([{service:"clean",spans:20,error_rate:0}]);
 expect(service.eligible).toBe(true);expect(service.score).toBe(0);
});
