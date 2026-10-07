import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fanoutThemeConfig } from "../../../theme";
import { chartThemeFor } from "../../../panels/compile";
import { contrastRatio } from "../../../panels/style";
import type { Frame, Panel, PanelResult } from "../../../panels/types";
import { PanelCard } from "./panel-card";
import { TableViz } from "./viz/table";
import { RowPanel } from "./viz/row-panel";
vi.mock("./echart-canvas",()=>({EChartCanvas:()=>null}));
let host: HTMLDivElement, root: Root;
beforeEach(()=>{vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);host=document.createElement("div");document.body.append(host);root=createRoot(host);});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();});
const mount = async (child:React.ReactNode,dark:boolean) => act(async()=>root.render(<MantineProvider theme={fanoutThemeConfig} forceColorScheme={dark?"dark":"light"}>{child}</MantineProvider>));
const panel:Panel={id:"logs",title:"Recent logs",viz:"logs",query:{from:"logs"},options:{highlight:"error"}};
const id="0123456789abcdef0123456789abcdef";
const body="Connection ERROR <*> <str> <num> <ip> <time> token=[REDACTED] "+"very long message ".repeat(25);
const frame:Frame={columns:[{name:"time",type:"time",role:"time"},{name:"severity",type:"string",role:"dimension"},{name:"service",type:"string",role:"dimension"},{name:"body",type:"string",role:"dimension"},{name:"trace_id",type:"string",role:"dimension"},{name:"namespace",type:"string",role:"dimension"}],values:[[Date.now(),Date.now()],["ERROR","ERROR"],["checkout","payments"],[body,"short message"],[id,""],["otel-demo","otel-demo"]],rows:2};
const result:PanelResult={id:"logs",status:"ok",elapsed_ms:1,frame};
it.each([false,true])("V6: highlight chip explains soft marks and constant namespace in subtitle, dark=%s", async dark=>{
 await mount(<PanelCard panel={panel} title={panel.title} result={result} loading={false} height={350} group="g" editing={false} agentAvailable={false} onView={()=>{}} onCopyLink={()=>{}} onExplain={()=>{}}/>,dark);
 expect(host.querySelector("[data-highlight-term]")?.textContent).toBe("highlight: error");
 expect(host.querySelector("[data-panel-subtitle]")?.textContent).toBe("logs · otel-demo");
 expect([...host.querySelectorAll("thead th")].map(e=>e.textContent)).not.toContain("namespace");
 expect([...host.querySelectorAll("thead th")].map(e=>e.textContent)).toContain("severity");
 const mark=host.querySelector<HTMLElement>("mark")!;
 expect(mark.textContent).toBe("ERROR"); expect(mark.style.background).toContain(dark?"0.22":"0.3");
 expect(mark.style.color).toBe("inherit"); expect(mark.style.borderRadius).toBe("3px");
});
it.each([false,true])("V6: log body owns remaining width, clamps then expands; trace first16/full title, dark=%s",async dark=>{
 await mount(<RowPanel panel={panel} result={result} height={350} dark={dark}/>,dark);
 const row=host.querySelector<HTMLTableRowElement>("tbody tr")!;
 const text=row.querySelector<HTMLElement>("[data-row-text]")!;
 expect(text.style.maxHeight).toBe("36px"); expect(text.style.whiteSpace).toBe("normal");
 expect(host.querySelector<HTMLTableColElement>('col[data-field="body"]')?.style.width).toBe("");
 expect(Number.parseFloat(host.querySelector<HTMLTableElement>("table")!.style.minWidth)).toBeGreaterThanOrEqual(656);
 const trace=row.querySelector<HTMLElement>("[data-trace-id]")!; expect(trace.textContent).toBe(id.slice(0,16));expect(trace.title).toBe(id);
 await act(async()=>row.click()); expect(row.dataset.rowExpanded).toBe("true"); expect(text.style.maxHeight).toBe("");
 await act(async()=>row.dispatchEvent(new KeyboardEvent("keydown",{key:"Enter",bubbles:true}))); expect(row.dataset.rowExpanded).toBe("false");
});
it.each([false,true])("V6: templates use five accent chips, numeric dimensions align, trace links clip, dark=%s",async dark=>{
 const p:Panel={id:"t",title:"Table",viz:"table",options:{columns:[{field:"template",format:"log_template"},{field:"duration",format:"bar",unit:"ms"},{field:"trace",format:"trace_link"}]}};
 const f:Frame={columns:[{name:"template",type:"string",role:"dimension"},{name:"duration",type:"number",role:"measure",unit:"ms"},{name:"attempts",type:"number",role:"dimension"},{name:"trace",type:"string",role:"dimension"}],values:[[body],[1450],[3],[id]],rows:1};
 await mount(<TableViz panel={p} result={{...result,frame:f}} height={350}/>,dark);
 expect([...host.querySelectorAll("[data-template-chip]")].map(e=>e.textContent)).toEqual(["<*>","<str>","<num>","<ip>","<time>"]);
 expect(host.querySelector("mark")).toBeNull();
 const cells=host.querySelectorAll<HTMLElement>("tbody td"); expect(cells[1].textContent).toContain("1.45 s"); expect(cells[2].style.textAlign).toBe("right");
 expect(cells[2].querySelector<HTMLElement>("p")?.style.fontFamily).toContain("monospace");
 expect(cells[3].textContent).toBe(id.slice(0,16)); expect(cells[3].querySelector("[title]")?.getAttribute("title")).toBe(id);
});
it("V6: empty/mixed/null constants are retained and severity is never hidden",async()=>{
 await mount(<RowPanel panel={panel} result={{...result,frame:{...frame,values:[...frame.values.slice(0,5),["otel-demo",null]]}}} height={350} dark={false}/>,false);
 expect([...host.querySelectorAll("thead th")].map(e=>e.textContent)).toContain("namespace");
});

it.each([false,true])("V7: pattern severity, chips, service, count and area trend in preview order, dark=%s",async dark=>{
 const p:Panel={id:"p",title:"Patterns",viz:"log_patterns",query:{from:"logs",by:["body_template"],measures:["count()"]}};
 const f:Frame={columns:[{name:"body_template",type:"string",role:"dimension"},{name:"count",type:"number",role:"measure",unit:"count"},{name:"trend",type:"json",role:"dimension"},{name:"severity",type:"string",role:"dimension"},{name:"service",type:"string",role:"dimension"}],values:[["failed <*> <num>","warning <str>","info <time>"],[12,8,3],["[1,3,8]","[1,2,5]","[1,1,1]"],["ERROR","WARN","INFO"],["payments","checkout","frontend"]],rows:3,trend:{start_ms:0,step_ms:60000}};
 await mount(<RowPanel panel={p} result={{...result,frame:f}} height={350} dark={dark}/>,dark);
 expect([...host.querySelectorAll("thead th")].map(e=>e.textContent)).toEqual(["severity","body_template","service","count","trend"]);
 expect([...host.querySelectorAll("tbody tr")].map(r=>r.querySelector("td")?.textContent)).toEqual(["◆ ERROR","■ WARN","● INFO"]);
 expect(host.querySelectorAll("[data-template-chip]")).toHaveLength(4);
 expect(host.querySelectorAll("[data-pattern-area]")).toHaveLength(3);
 expect(host.querySelector('svg[aria-label="Pattern count trend"] title')?.textContent).toBe("1970-01-01T00:00:00.000Z: 1");
});

it.each([false,true])("V9: formatted status badges use readable text and soft backgrounds, dark=%s",async dark=>{
 const p:Panel={id:"p",title:"Health",viz:"table",options:{columns:[{field:"health",format:"status"}]}};
 await mount(<TableViz panel={p} result={{...result,frame:{columns:[{name:"health",type:"string",role:"dimension"}],values:[["unhealthy","degraded"]],rows:2}}} height={200}/>,dark);
 for(const badge of host.querySelectorAll<HTMLElement>(".mantine-Badge-root")) {
  expect(badge.style.color).toBe(chartThemeFor(dark).text);
  expect(badge.style.background).toContain("0.14");
 }
});

it.each([false,true])("V9: semantic table ink reaches 4.5:1, severity badge uses text tokens, dark=%s",async dark=>{
 const p:Panel={id:"p",title:"Counts",viz:"table",thresholds:[{value:10,status:"warn"},{value:20,status:"bad"}],better:"lower"};
 await mount(<TableViz panel={p} result={{...result,frame:{columns:[{name:"n",type:"number",role:"measure"}],values:[[12]],rows:1}}} height={200}/>,dark);
 const ink=host.querySelector<HTMLElement>("tbody p")!.style.color;
 const hex=ink.startsWith("#") ? ink : "#"+(ink.match(/\d+/g)??[]).map(n=>Number(n).toString(16).padStart(2,"0")).join("");
 expect(contrastRatio(hex,chartThemeFor(dark).surface)).toBeGreaterThanOrEqual(4.5);
 await mount(<RowPanel panel={panel} result={result} height={200} dark={dark}/>,dark);
 const badge=host.querySelector<HTMLElement>(".mantine-Badge-root")!;
 expect(badge.style.color).toBe(chartThemeFor(dark).text);
 expect(badge.style.background).toContain("0.14");
});

it.each([false,true])("Q2: short OTLP labels, full titles and isolated service/count columns (%s)",async dark=>{
 const severities=["TRACE4","DEBUG2","INFO3","WARN4","ERROR2","FATAL3","UNSPECIFIED","surprise","SEVERITY_NUMBER_INFO2","17"];
 const labels=["TRACE","DEBUG","INFO","WARN","ERROR","FATAL","—","—","INFO","ERROR"];
 const services=severities.map((_,i)=>"product-catalog-with-a-very-long-service-name-"+i);
 const p:Panel={id:"p",title:"Patterns",viz:"log_patterns",query:{from:"logs",by:["body_template"],measures:["count()"]}};
 const f:Frame={columns:[{name:"severity",type:"string",role:"dimension"},{name:"body_template",type:"string",role:"dimension"},{name:"service",type:"string",role:"dimension"},{name:"count",type:"number",role:"measure",unit:"count"},{name:"trend",type:"json",role:"dimension"}],values:[severities,severities.map(()=>"failed <*>"),services,severities.map(()=>75900),severities.map(()=>"[1,3]")],rows:severities.length};
 await mount(<RowPanel panel={p} result={{...result,frame:f}} height={350} dark={dark} onSelect={()=>{}}/>,dark);
 const badges=[...host.querySelectorAll<HTMLElement>(".mantine-Badge-root")];
 expect(badges.map(b=>b.textContent?.replace(/^[◆■●] /,""))).toEqual(labels);
 expect(badges.map(b=>b.title)).toEqual(severities);
 const service=host.querySelector<HTMLElement>('tbody td[data-field="service"]')!;
 expect(parseFloat(service.style.minWidth)).toBeGreaterThanOrEqual(140);
 const value=service.querySelector<HTMLElement>("[title]")!;
 expect(value.title).toBe(services[0]);expect(value.style.textOverflow).toBe("ellipsis");expect(value.style.overflow).toBe("hidden");expect(value.style.whiteSpace).toBe("nowrap");
 const count=host.querySelector<HTMLElement>('tbody td[data-field="count"]')!;
 expect(count.style.textAlign).toBe("right");expect(parseFloat(count.style.paddingLeft)).toBeGreaterThanOrEqual(12);expect(count.style.overflow).toBe("hidden");
 expect(host.querySelector<HTMLTableColElement>('col[data-field="count"]')?.style.width).toBe("140px");
});

it.each([false,true])("Q3: one padded muted footer stacks hint before distinct data notes (%s)",async dark=>{
 const p:Panel={id:"t",title:"Traces",viz:"traces",drill:"traces"};
 const note="Service is All; showing the unsplit whole-window frame.";
 const r:PanelResult={...result,frame:{...frame,truncated:true,note},annotation_error:note,annotation_scope:{limited:true,services:[]}};
 await mount(<PanelCard panel={p} title={p.title} result={r} loading={false} height={350} group="g" editing={false} agentAvailable={false} onView={()=>{}} onCopyLink={()=>{}} onExplain={()=>{}}/>,dark);
 const body=host.querySelector<HTMLElement>("[data-panel-body]")!,footer=host.querySelector<HTMLElement>("[data-panel-notes]")!;
 expect(footer).not.toBeNull();expect(body.classList.contains("dashboard-panel-padding")).toBe(true);expect(footer.classList.contains("dashboard-panel-padding")).toBe(true);
 const notes=[...footer.querySelectorAll<HTMLElement>("[data-panel-note]")];
 expect(notes[0].textContent).toBe("Click a row to open the trace.");
 expect(notes.map(n=>n.textContent)).toEqual(["Click a row to open the trace.","Truncated: showing limited data",note,"Annotation service scope is limited."]);
 for(const n of notes){expect(n.style.fontSize).toBe("calc(0.75rem * var(--mantine-scale))");expect(n.style.color).toBe("var(--mantine-color-dimmed)");}
 expect(host.textContent).not.toContain("Showing 2 rows.");
});

it("Q3: SQL sparkline feedback does not repeat as a footer",async()=>{
 const p:Panel={id:"t",title:"Table",viz:"table",sql:"SELECT 1 AS n",options:{columns:[{field:"n",format:"sparkline"}]}};
 await mount(<PanelCard panel={p} title={p.title} result={{...result,frame:{columns:[{name:"n",type:"number",role:"measure"}],values:[[1]],rows:1}}} loading={false} height={350} group="g" editing={false} agentAvailable={false} onView={()=>{}} onCopyLink={()=>{}} onExplain={()=>{}}/>,false);
 expect(host.textContent?.split("Sparkline requires an array column")).toHaveLength(2);
});
