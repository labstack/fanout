import { expect, it } from "vitest";
import { fitServiceMap, layoutServiceMapRaw, serviceCardLabels } from "./viz/service-map-layout";

const names = ["ad", "cart", "checkout", "currency", "email", "flagd", "fraud-detection", "frontend", "frontend-proxy", "image-provider", "kafka", "load-generator", "payment", "product-catalog", "product-reviews", "quote", "recommendation", "shipping"];
const nodes = names.map(id => ({ id, health: "healthy" as const, spans: 10, p95_ms: 20, error_rate: 0, request_rate: 1 }));

it.each([{ width: 1120, height: 420 }, { width: 520, height: 360 }])("wraps 18 isolated services into centred, readable rows inside $width × $height", size => {
  const model = { nodes, edges: [] };
  // Reuse the same raw layout: wrapping must follow viewport resizing too.
  const raw = layoutServiceMapRaw(model, { width: 1120, height: 420 });
  const graph = fitServiceMap(raw, model, size);
  expect(graph.nodes).toHaveLength(18);
  expect(graph.initialView).toEqual({ x: 0, y: 0 });
  const rows = new Map<number, typeof graph.nodes>();
  for (const node of graph.nodes) {
    expect(node.x).toBeGreaterThanOrEqual(12);
    expect(node.y).toBeGreaterThanOrEqual(12);
    expect(node.x + node.width).toBeLessThanOrEqual(size.width - 12);
    expect(node.y + node.height).toBeLessThanOrEqual(size.height - 12);
    const label = serviceCardLabels(node, { width: node.width / graph.scale, scale: graph.scale, compact: graph.compact });
    expect(label.nameSize * graph.scale).toBeGreaterThanOrEqual(11);
    for (const other of graph.nodes) if (node.id !== other.id) {
      expect(node.x >= other.x + other.width || other.x >= node.x + node.width || node.y >= other.y + other.height || other.y >= node.y + node.height).toBe(true);
    }
    rows.set(node.y, [...(rows.get(node.y) ?? []), node]);
  }
  expect(rows.size).toBeGreaterThan(1);
  for (const row of rows.values()) {
    const left = Math.min(...row.map(n => n.x)), right = Math.max(...row.map(n => n.x + n.width));
    expect((left + right) / 2).toBeCloseTo(size.width / 2);
  }
  expect(graph.uncalledLabel!.y + 12).toBeLessThan(Math.min(...graph.nodes.map(n => n.y)));
});

it("wraps isolated services below a routed graph without overlapping its routes", () => {
  const size = { width: 520, height: 360 };
  const model = { nodes, edges: [{ id: "ad->cart", caller: "ad", callee: "cart", edge_type: "call", calls: 10, average_ms: 5, error_rate: 0, request_rate: 1, status: null }] };
  const graph = fitServiceMap(layoutServiceMapRaw(model, size), model, size);
  const isolated = graph.nodes.filter(n => n.uncalled);
  expect(new Set(isolated.map(n => n.y)).size).toBeGreaterThan(1);
  for (const node of graph.nodes) {
    expect(node.x).toBeGreaterThanOrEqual(12);
    expect(node.x + node.width).toBeLessThanOrEqual(size.width - 12);
    expect(node.y + node.height).toBeLessThanOrEqual(size.height - 12);
  }
  expect(graph.uncalledLabel!.y).toBeGreaterThan(Math.max(...graph.edges.flatMap(e => e.points.map(p => p.y))));
  expect(graph.uncalledLabel!.y + 12).toBeLessThan(Math.min(...isolated.map(n => n.y)));
});
