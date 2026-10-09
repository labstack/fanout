import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { serviceMapModel } from "../../../panels/rollups";
import { wallFrame } from "../../tests/service-map-wall";
import { assertServiceMapDOM } from "../../tests/service-map-collector";
import { fitServiceMap, layoutServiceMapRaw } from "./viz/service-map-layout";
import { ServiceMapViz } from "./viz/service-map";

it.each([{width:460,height:360},{width:760,height:360},{width:1100,height:420}])("keeps the wall readable with a separate isolated note and stable Fit transform at $width × $height", async size => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const model = serviceMapModel(wallFrame), graph = fitServiceMap(layoutServiceMapRaw(model, size), model, size);
  expect(graph.nodes).toHaveLength(18); expect(graph.edges).toHaveLength(23);
  expect(graph.scale).toBeGreaterThanOrEqual(.65);
  const entry=graph.nodes.find(n=>n.entry)!;
  expect(entry.x+entry.width/2+graph.initialView.x).toBeGreaterThanOrEqual(8);
  expect(entry.x+entry.width/2+graph.initialView.x).toBeLessThanOrEqual(size.width-8);
  expect(entry.y+entry.height/2+graph.initialView.y).toBeGreaterThanOrEqual(8);
  expect(entry.y+entry.height/2+graph.initialView.y).toBeLessThanOrEqual(size.height-8);
  for (const n of graph.nodes) {
    expect(n.x).toBeGreaterThanOrEqual(8);
    expect(n.y).toBeGreaterThanOrEqual(8);
    expect(n.x + n.width).toBeLessThanOrEqual(graph.contentWidth - 8);
    expect(n.y + n.height).toBeLessThanOrEqual(graph.contentHeight - 8);
  }
  const original = HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function(this: HTMLElement) {
    if (this.hasAttribute("data-service-viewport")) return DOMRect.fromRect(size);
    if (this.hasAttribute("data-service-node")) return DOMRect.fromRect({x:parseFloat(this.style.left),y:parseFloat(this.style.top),width:parseFloat(this.style.width),height:parseFloat(this.style.height)});
    if (this.hasAttribute("data-service-uncalled-label")) return DOMRect.fromRect({x:graph.uncalledLabel!.x,y:graph.uncalledLabel!.y,width:174,height:11});
    return original.call(this);
  });
  vi.spyOn(SVGElement.prototype,"getBoundingClientRect").mockImplementation(function(this: SVGElement) {
    const edge = graph.edges.find(e => e.path === this.getAttribute("d"));
    if (!edge) return DOMRect.fromRect();
    const xs=edge.points.map(p=>p.x),ys=edge.points.map(p=>p.y);
    return DOMRect.fromRect({x:Math.min(...xs),y:Math.min(...ys),width:Math.max(...xs)-Math.min(...xs),height:Math.max(...ys)-Math.min(...ys)});
  });
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host), view=vi.fn();
  try {
    await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:wallFrame}} dark={false} height={size.height+24} onMapView={view}/></MantineProvider>));
    const content=host.querySelector<HTMLElement>('[data-service-content]')!, initial=content.style.transform;
    expect(initial).toBe(`translate(${graph.initialView.x}px, ${graph.initialView.y}px) scale(1)`);
    expect(view.mock.lastCall![0].canFit).toBe(false);
    expect(assertServiceMapDOM(host.querySelector('[data-service-viewport]')!,{requireNames:true,allowOverflow:true})).toMatchObject({nodes:18,edges:23});
    await act(async()=>view.mock.lastCall![0].fit());expect(content.style.transform).toBe(initial);
  } finally {await act(async()=>root.unmount());host.remove();vi.restoreAllMocks();vi.unstubAllGlobals();}
});
