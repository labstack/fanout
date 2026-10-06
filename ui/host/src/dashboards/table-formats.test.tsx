import {describe,expect,it,beforeEach,afterEach,vi} from "vitest";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import {columnDisplay} from "../../../panels/column-formats";
import {sparkline,statValue} from "../../../panels/frame";
import type {Frame,Panel,PanelResult} from "../../../panels/types";
import { TableViz } from "./viz/table";
import { StatViz } from "./viz/stat";
import { PanelGrid } from "./grid";
import { bad, warn, ok } from "../../../tokens";
import * as tableEngine from "@tanstack/react-table";
vi.mock("@tanstack/react-table", {spy:true});
const panel:Panel={id:"p",title:"P",viz:"timeseries",query:{from:"spans",where:["service = $service"],measures:["count()"]}};
const result:PanelResult={id:"p",status:"ok",elapsed_ms:1,from_ms:0,to_ms:10000,annotation_scope:{services:[{namespace:"shop",service:"checkout"}]}};
describe("annotations and formats",()=>{
 it("formats every column type and preserves whole-window stats",()=>{
  expect(columnDisplay({field:"n",format:"bar"},3,6)).toEqual({kind:"bar",text:"3",fraction:.5});
  expect(columnDisplay({field:"state",format:"status"},"bad",0).kind).toBe("status");
  const graded:Panel={id:"p",title:"P",viz:"table",thresholds:[{value:5,status:"warn"},{value:10,status:"bad"}],better:"lower"};
  expect(columnDisplay({field:"n",format:"status"},12,0,graded).status).toBe("bad");
  expect(columnDisplay({field:"n",format:"status"},2,0,graded).status).toBe("ok");
  expect(columnDisplay({field:"n",format:"status"},2,0,{...graded,better:"higher"}).status).toBe("bad");
  expect(columnDisplay({field:"n",format:"sparkline"},42,0,graded,undefined,[1,null,3]).points).toEqual([1,null,3]);
  expect(columnDisplay({field:"trend",format:"sparkline"},"[1,null,3]",0).points).toEqual([1,null,3]);
  expect(columnDisplay({field:"n",format:"sparkline"},42,0).unsupported).toBe("Sparkline requires an array column");
  expect(columnDisplay({field:"trace_id",format:"trace_link"},"abc",0).kind).toBe("trace_link");
  expect(columnDisplay({field:"service",format:"service_link",variable:"service"},"checkout",0).kind).toBe("service_link");
  expect(columnDisplay({field:"body_template",format:"log_template"},"failed <*>",0).text).toBe("failed <*>");
  const frame={columns:[{name:"time",type:"time" as const,role:"time" as const},{name:"count",type:"number" as const,role:"measure" as const}],values:[[0,1],[40,2]],rows:2,totals:[null,42]};
  expect(statValue({...panel,viz:"stat"},frame)).toBe(42);expect(sparkline(frame)).toEqual([40,2]);
 });
});

const cleanups: (() => void)[] = [];
beforeEach(() => {
 vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);
 vi.stubGlobal("ResizeObserver",class {observe(){} unobserve(){} disconnect(){}});
 vi.stubGlobal("IntersectionObserver",class {observe(){} unobserve(){} disconnect(){}});
});
afterEach(async () => {
 await act(async () => cleanups.splice(0).forEach(fn => fn()));
 vi.unstubAllGlobals();
 vi.restoreAllMocks();
 document.body.innerHTML="";
});
async function render(node:ReactNode) {
 const host=document.createElement("div");document.body.append(host);
 const root=createRoot(host);cleanups.push(()=>root.unmount());
 const rerender=async (node:ReactNode)=>{await act(async()=>root.render(<MantineProvider theme={{colors:{bad:[...bad],warn:[...warn],ok:[...ok]}}}>{node}</MantineProvider>));};
 await rerender(node);return {host,rerender};
}
const formattedPanel:Panel={id:"p",title:"Formats",viz:"table",query:{from:"spans",by:["service"],measures:["count()"]},thresholds:[{value:5,status:"warn"},{value:10,status:"bad"}],options:{columns:[
 {field:"service",format:"service_link",variable:"service"},
 {field:"trace",format:"trace_link"},
 {field:"count",format:"sparkline"},
 {field:"n",format:"bar"},
 {field:"state",format:"status"},
 {field:"latency",format:"unit"},
 {field:"body_template",format:"log_template"},
]}};
const formattedFrame:Frame={columns:[
 {name:"service",type:"string",role:"dimension"},
 {name:"trace",type:"string",role:"dimension"},
 {name:"count",type:"number",role:"measure"},
 {name:"n",type:"number",role:"measure"},
 {name:"state",type:"number",role:"measure"},
 {name:"latency",type:"number",role:"measure",unit:"ms"},
 {name:"body_template",type:"string",role:"dimension"},
 {name:"namespace",type:"string",role:"dimension"},
],values:[["checkout","payment"],["abc","def"],[12,2],[3,6],[12,2],[900,null],["token=[REDACTED] failed <*> <img src=x> <*>","failed <*>"],["shop","ops"]],rows:2,trends:{count:[[1,null,3],[1,2,3]]}};

it("grades numeric statuses with spec, result and threshold-derived direction in order",()=>{
 const format={field:"n",format:"status" as const};
 const graded={...formattedPanel,better:undefined};
 expect(columnDisplay(format,12,0,graded).status).toBe("bad");
 expect(columnDisplay(format,12,0,graded,"higher").status).toBe("ok");
 expect(columnDisplay(format,12,0,{...graded,better:"lower"},"higher").status).toBe("bad");
 expect(columnDisplay(format,2,0,{...graded,thresholds:[{value:10,status:"warn"},{value:5,status:"bad"}]}).status).toBe("bad");
 expect(columnDisplay(format,null,0,graded).text).toBe("—");
 expect(columnDisplay(format,Infinity,0,graded).status).toBeNull();
 expect(columnDisplay({field:"x",format:"sparkline"},"[1,\"2\",null,3]",0).points).toEqual([1,null,null,3]);
 expect(columnDisplay({field:"x",format:"sparkline"},"[]",0,graded,undefined,Array(300).fill(1)).points).toHaveLength(240);
});

it("renders all formats safely and keeps trends and current callbacks aligned after sorting",async()=>{
 const onPoint=vi.fn(),onVariable=vi.fn(),onSelect=vi.fn();
 const props={panel:formattedPanel,result:{...result,frame:formattedFrame},height:300,onPoint,onVariable,onSelect};
 const {host,rerender}=await render(<TableViz {...props}/>);
 expect(host.querySelectorAll('svg[role="img"]')).toHaveLength(2);
 expect(host.querySelector("tbody svg path")?.getAttribute("d")).toBe("M0.0,30.0M200.0,2.0");
 expect(host.querySelector("tbody tr td:nth-child(4)")?.textContent).toBe("3");
 expect(host.querySelector<HTMLElement>("tbody tr td:nth-child(4) div div div")?.style.width).toBe("50%");
 expect(host.querySelector("tbody tr td:nth-child(5)")?.textContent).toBe("◆ 12");
 expect(host.querySelector("tbody tr td:nth-child(6)")?.textContent).toBe("900ms");
 expect(host.querySelector("tbody tr:nth-child(2) td:nth-child(6)")?.textContent).toBe("—");
 expect([...host.querySelectorAll("mark")].map(mark=>mark.textContent)).toEqual(["<*>","<*>","<*>"]);
 expect(host.querySelector("img,script")).toBeNull();
 expect(host.textContent).toContain("token=[REDACTED] failed <*> <img src=x> <*>");
 await act(async()=>host.querySelector<HTMLButtonElement>("th:nth-child(3) button")!.click());
 await act(async()=>host.querySelector<HTMLButtonElement>("th:nth-child(3) button")!.click());
 expect(host.querySelector("tbody tr td")?.textContent).toBe("payment");
 expect(host.querySelector("tbody svg path")?.getAttribute("d")).toBe("M0.0,30.0L100.0,16.0L200.0,2.0");
 const nextPoint=vi.fn(),nextVariable=vi.fn();
 await rerender(<TableViz {...props} onPoint={nextPoint} onVariable={nextVariable}/>);
 const link=host.querySelector<HTMLAnchorElement>("tbody tr a")!;
 const target=JSON.parse(new URL(link.href).searchParams.get("drill")!);
 expect(target).toMatchObject({panel_id:"p",kind:"traces",trace_id:"def",namespace:"ops",dimensions:{service:"payment"},window_from:new Date(0).toISOString(),window_to:new Date(10000).toISOString()});
 const modified=new MouseEvent("click",{bubbles:true,cancelable:true,ctrlKey:true});
 await act(async()=>link.dispatchEvent(modified));
 expect(modified.defaultPrevented).toBe(false);expect(nextPoint).not.toHaveBeenCalled();
 await act(async()=>link.click());
 expect(nextPoint).toHaveBeenCalledOnce();expect(nextPoint.mock.calls[0][0]).toMatchObject({trace_id:"def",namespace:"ops",dimensions:{service:"payment"}});
 await act(async()=>host.querySelector<HTMLButtonElement>("tbody tr button")!.click());
 expect(nextVariable.mock.calls).toEqual([["service","payment"]]);
 expect(onPoint).not.toHaveBeenCalled();expect(onVariable).not.toHaveBeenCalled();expect(onSelect).not.toHaveBeenCalled();
});

it.each([ ["number",42], ["string","[1,2]"], ["json","42"], ["json","{\"a\":1}"], ["json","invalid"] ] as const)("shows cell and panel unsupported notes for SQL %s %s",async(type,value)=>{
 const frame:Frame={columns:[{name:"trend",type,role:"dimension"}],values:[[value]],rows:1};
 const {host}=await render(<TableViz panel={{id:"p",title:"SQL",viz:"table",sql:"SELECT 1",options:{columns:[{field:"trend",format:"sparkline"},{field:"gone",format:"unit"}]}}} result={{...result,frame}} height={200}/>);
 expect([...host.querySelectorAll('[role="status"]')].filter(node=>node.textContent==="Sparkline requires an array column")).toHaveLength(2);
 expect(host.textContent).toContain("Column formats unavailable: gone");
 expect(host.textContent).not.toContain("No trend");
});

it("accepts described SQL arrays and preserves custom cell overrides",async()=>{
 const frame:Frame={columns:[{name:"trend",type:"json",role:"dimension"}],values:[["[1,null,3]"]],rows:1};
 const props={panel:{id:"p",title:"SQL",viz:"table" as const,sql:"SELECT 1",options:{columns:[{field:"trend",format:"sparkline" as const}]}},result:{...result,frame},height:200};
 const {host,rerender}=await render(<TableViz {...props}/>);
 expect(host.querySelector("svg path")?.getAttribute("d")).toBe("M0.0,30.0M200.0,2.0");
 expect(host.querySelector('[role="status"]')).toBeNull();
 await rerender(<TableViz {...props} renderCell={()=> <span>Custom cell</span>}/>);
 expect(host.textContent).toContain("Custom cell");expect(host.querySelector("svg")).toBeNull();
});

it("shows No trend for structured measures when their trend is unavailable",async()=>{
 const {host}=await render(<TableViz panel={formattedPanel} result={{...result,frame:{...formattedFrame,trends:undefined}}} height={200}/>);
 expect(host.textContent).toContain("No trend");
 expect(host.textContent).not.toContain("Sparkline requires an array column");
});

it("uses accessible gap-preserving stat trends while retaining whole-window totals and delta",async()=>{
 const frame:Frame={columns:[{name:"time",type:"time",role:"time"},{name:"count",type:"number",role:"measure"}],values:[[0,1,2],[1,null,3]],rows:3,totals:[null,42]};
 const {host}=await render(<StatViz panel={{...panel,viz:"stat"}} result={{...result,frame,previous:{...frame,totals:[null,21]}}}/>);
 expect(host.textContent).toContain("42");expect(host.textContent).toContain("+100%");
 expect(host.querySelector('svg[role="img"]')?.getAttribute("aria-label")).toBe("Value over this panel's time range");
 expect(host.querySelector("svg path")?.getAttribute("d")).toBe("M0.0,30.0M200.0,2.0");
});

it("threads service and trace actions from the grid and keeps Inspect available",async()=>{
 const onVariable=vi.fn(),onPoint=vi.fn();const client=new QueryClient();cleanups.push(()=>client.clear());
 const {host}=await render(<QueryClientProvider client={client}><PanelGrid dashboardId="d" version={1} spec={{version:1,name:"Formats",time:{range:"1h"},panels:[formattedPanel]}} vars={{}} results={new Map([["p",{...result,frame:formattedFrame}]])} fetching={false} editing={false} agentAvailable={false} onOpenChat={vi.fn()} onVariable={onVariable} onPoint={onPoint} onView={vi.fn()} onVisible={vi.fn()}/></QueryClientProvider>);
 await act(async()=>host.querySelector<HTMLButtonElement>("tbody tr button")!.click());
 expect(onVariable.mock.calls).toEqual([["service","checkout"]]);
 await act(async()=>host.querySelector<HTMLAnchorElement>("tbody tr a")!.click());
 expect(onPoint).toHaveBeenCalledWith(formattedPanel,expect.objectContaining({trace_id:"abc"}));
 await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Formats menu"]')!.click());
 await vi.waitFor(()=>expect(document.querySelector('[role="menuitem"]')).not.toBeNull());
 await act(async()=>[...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(node=>node.textContent==="Inspect")!.click());
 await vi.waitFor(()=>expect(document.querySelector('[role="dialog"] table')).not.toBeNull());
 expect(document.querySelectorAll('[role="dialog"] tbody tr')).toHaveLength(2);
});

it("keeps column definitions when grid callback identities change with the same data",async()=>{
 const useTable=vi.mocked(tableEngine.useTable);
 useTable.mockClear();
 const client=new QueryClient();cleanups.push(()=>client.clear());
 const props={dashboardId:"d",version:1,spec:{version:1 as const,name:"Formats",time:{range:"1h"},panels:[formattedPanel]},vars:{},results:new Map([["p",{...result,frame:formattedFrame}]]),fetching:false,editing:false,agentAvailable:false,onOpenChat:vi.fn(),onVariable:vi.fn(),onPoint:vi.fn(),onView:vi.fn(),onVisible:vi.fn()};
 const grid=(callbacks:Partial<typeof props>={})=><QueryClientProvider client={client}><PanelGrid {...props} {...callbacks}/></QueryClientProvider>;
 const {host,rerender}=await render(grid());
 const columns=useTable.mock.calls.at(-1)![0].columns;
 const onPoint=vi.fn(),onVariable=vi.fn();
 useTable.mockClear();
 await rerender(grid({onPoint,onVariable}));
 expect(useTable).toHaveBeenCalled();
 for(const [options] of useTable.mock.calls) expect(options.columns).toBe(columns);
 await act(async()=>host.querySelector<HTMLButtonElement>("tbody tr button")!.click());
 await act(async()=>host.querySelector<HTMLAnchorElement>("tbody tr a")!.click());
 expect(onVariable).toHaveBeenCalledWith("service","checkout");
 expect(onPoint).toHaveBeenCalledWith(formattedPanel,expect.objectContaining({trace_id:"abc"}));
 expect(props.onVariable).not.toHaveBeenCalled();expect(props.onPoint).not.toHaveBeenCalled();
});

it.each(["Table trends are limited by the cell budget.","Some row trends were omitted to stay within the response budget."])("does not claim rows were cut for trend-only truncation: %s",async note=>{
 const {host}=await render(<TableViz panel={formattedPanel} result={{...result,frame:{...formattedFrame,truncated:true,note}}} height={200}/>);
 expect(host.querySelectorAll("tbody tr")).toHaveLength(formattedFrame.rows);
 expect(host.textContent).not.toContain("Showing the first");
});
