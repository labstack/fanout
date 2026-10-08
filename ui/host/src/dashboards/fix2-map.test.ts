import { expect, it } from "vitest";
import { serviceMapModel } from "../../../panels/rollups";
import { fitServiceMap, layoutServiceMapRaw } from "./viz/service-map-layout";
import { demoFrame } from "../../tests/service-map-demo";
const model = serviceMapModel(demoFrame, { from_ms: 0, to_ms: 3600000 });
const crosses = (a: {x:number;y:number}, b: {x:number;y:number}, n: {x:number;y:number;width:number;height:number}) => {
  let lo=0,hi=1;
  for (const [value,delta,min,max] of [[a.x,b.x-a.x,n.x+.1,n.x+n.width-.1],[a.y,b.y-a.y,n.y+.1,n.y+n.height-.1]]) {
    if (!delta) { if (value < min || value > max) hi=-1; }
    else { const p=(min-value)/delta,q=(max-value)/delta;lo=Math.max(lo,Math.min(p,q));hi=Math.min(hi,Math.max(p,q)); }
  }
  return lo<hi;
};
it("routes every demo edge around other node boxes", () => {
  const raw=layoutServiceMapRaw(model,{width:780,height:460});
  const graph=fitServiceMap(raw,model,{width:780,height:460});
  for(const edge of graph.edges) for(let i=1;i<edge.points.length;i++) for(const node of graph.nodes) if(node.id!==edge.caller&&node.id!==edge.callee) expect(crosses(edge.points[i-1],edge.points[i],node),`${edge.caller} → ${edge.callee} crosses ${node.id}`).toBe(false);
});
it.each([{ width: 780, height: 460 }, { width: 1100, height: 220 }])("contains the LR demo in its pan canvas with no overlapping boxes at $width × $height", size => {
  const raw = layoutServiceMapRaw(model, size), graph = fitServiceMap(raw, model, size);
  expect(graph.nodes).toHaveLength(20);
  for (const node of graph.nodes) {
    expect(node.x).toBeGreaterThanOrEqual(8); expect(node.y).toBeGreaterThanOrEqual(8);
    expect(node.x + node.width).toBeLessThanOrEqual(graph.contentWidth - 8);
    expect(node.y + node.height).toBeLessThanOrEqual(graph.contentHeight - 8);
    for (const other of graph.nodes) if (node.id !== other.id) expect(node.x >= other.x + other.width || other.x >= node.x + node.width || node.y >= other.y + other.height || other.y >= node.y + node.height).toBe(true);
  }
  for (const edge of model.edges) {
    const caller = graph.nodes.find(node => node.id === edge.caller)!, callee = graph.nodes.find(node => node.id === edge.callee)!;
    expect(caller.x + caller.width).toBeLessThan(callee.x);
  }
  for (const edge of graph.edges) for (const point of edge.points) {
    expect(point.x).toBeGreaterThanOrEqual(8); expect(point.y).toBeGreaterThanOrEqual(8);
    expect(point.x).toBeLessThanOrEqual(graph.contentWidth - 8); expect(point.y).toBeLessThanOrEqual(graph.contentHeight - 8);
  }
  const width = Math.max(...graph.nodes.map(n => n.x + n.width)) - Math.min(...graph.nodes.map(n => n.x));
  const height = Math.max(...graph.nodes.map(n => n.y + n.height)) - Math.min(...graph.nodes.map(n => n.y));
  expect(Math.max(width / size.width, height / size.height)).toBeGreaterThanOrEqual(.7);
  expect(graph.scale).toBeGreaterThanOrEqual(.65);
  expect(graph.scale).toBeLessThanOrEqual(1);
  for(const n of graph.nodes.filter(n=>n.entry)) {expect(n.x+graph.initialView.x).toBeGreaterThanOrEqual(8);expect(n.y+graph.initialView.y).toBeGreaterThanOrEqual(8);expect(n.x+n.width+graph.initialView.x).toBeLessThanOrEqual(size.width-8);expect(n.y+n.height+graph.initialView.y).toBeLessThanOrEqual(size.height-8);}
});
it("uses identical Dagre ranks irrespective of card size or input row order", () => {
  const reversed = { ...model, nodes: [...model.nodes].reverse(), edges: [...model.edges].reverse() };
  expect(layoutServiceMapRaw(model, { width: 780, height: 460 })).toEqual(layoutServiceMapRaw(reversed, { width: 1100, height: 220 }));
});
