import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { fitServiceMap, layoutServiceMapRaw } from "./viz/service-map-layout";
import { ServiceMapViz } from "./viz/service-map";
const node=(id:string)=>({id,health:"healthy" as const,spans:10,p95_ms:20,error_rate:0,request_rate:1});
const edge=(a:string,b:string)=>({id:`${a}->${b}`,caller:a,callee:b,edge_type:"call",calls:10,average_ms:5,error_rate:0,request_rate:1,status:null});
const size={width:1100,height:220};
it.each([1,2,3])("caps %s-service fit at natural scale and centres it",n=>{
 const nodes=Array.from({length:n},(_,i)=>node(`svc-${i}`)),edges=nodes.slice(1).map((s,i)=>edge(nodes[i].id,s.id)),m={nodes,edges};
 const fit=fitServiceMap(layoutServiceMapRaw(m,size),m,size);expect(fit.scale).toBe(1);
 for(const card of fit.nodes) {expect(card.width).toBeLessThanOrEqual(200);expect(card.height).toBe(44);}
 const xs=fit.nodes.flatMap(n=>[n.x,n.x+n.width]);expect((Math.min(...xs)+Math.max(...xs))/2).toBeCloseTo(size.width/2);
});
it("centres an overflowing 20-service fanout on its visible entry at the micro floor",()=>{
 const nodes=[node("gateway"),...Array.from({length:19},(_,i)=>node(`service-${i}-backend`))],m={nodes,edges:nodes.slice(1).map(n=>edge("gateway",n.id))};
 const fit=fitServiceMap(layoutServiceMapRaw(m,size),m,size);expect(fit.scale).toBe(.65);expect(fit.contentHeight).toBeGreaterThan(220);
 const root=fit.nodes.find(n=>n.entry)!;expect(root.y+fit.initialView.y).toBeGreaterThanOrEqual(0);expect(root.y+root.height+fit.initialView.y).toBeLessThanOrEqual(220);
 const leaves=fit.nodes.filter(n=>!n.entry).sort((a,b)=>a.y-b.y);
 for(let i=0;i<leaves.length;i++){expect(leaves[i].height).toBeGreaterThanOrEqual(13);if(i)expect(leaves[i].y-leaves[i-1].y).toBeGreaterThanOrEqual(13);}
});
let cleanup=()=>{};
afterEach(async()=>{await act(async()=>cleanup());cleanup=()=>{};vi.restoreAllMocks();vi.unstubAllGlobals();});
it("keeps dense fanout labels in their boxes and allows pan then Fit",async()=>{
 vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);
 vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(()=>DOMRect.fromRect(size));
 const names=["gateway",...Array.from({length:19},(_,i)=>`service-${i}-backend`)];
 const cols=["kind","service","caller","callee","health","spans"];
 const rows=[...names.map(n=>["node",n,"","","healthy",10]),...names.slice(1).map(n=>["edge","","gateway",n,"",10])];
 const frame={rows:rows.length,columns:cols.map(name=>({name,type:"string" as const,role:"dimension" as const})),values:cols.map((_,i)=>rows.map(r=>r[i]))};
 const host=document.createElement("div"),root=createRoot(host);cleanup=()=>root.unmount();const view=vi.fn();
 await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:0,frame}} dark={false} height={244} onMapView={view}/></MantineProvider>));
 const viewport=host.querySelector<HTMLElement>('[data-service-viewport]')!,content=host.querySelector<HTMLElement>('[data-service-content]')!,initial=content.style.transform;
 for(const card of host.querySelectorAll<HTMLElement>('[data-service-node]')) expect(parseFloat(card.style.height)).toBeGreaterThanOrEqual(parseFloat(card.querySelector<HTMLElement>('[data-service-text]')!.style.fontSize)+2);
 await act(async()=>{viewport.dispatchEvent(new PointerEvent("pointerdown",{button:0,clientY:100,bubbles:true}));viewport.dispatchEvent(new PointerEvent("pointermove",{clientY:-100,bubbles:true}));viewport.dispatchEvent(new PointerEvent("pointerup",{bubbles:true}));});
 expect(content.style.transform).not.toBe(initial);await act(async()=>view.mock.lastCall![0].fit());expect(content.style.transform).toBe(initial);
});
