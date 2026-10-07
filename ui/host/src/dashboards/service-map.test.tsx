import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { serviceMapModel } from "../../../panels/rollups";
import type { Frame } from "../../../panels/types";
import { ServiceMapViz, layoutServiceMap } from "./viz/service-map";
import { PanelCard } from "./panel-card";
import { chartThemeFor } from "../../../panels/compile";
import { makeDrill } from "./drill-state";
vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div role="img" aria-label={label} /> }));

// OpenTelemetry demo topology: twenty services, twenty-three directed routes.
const names = ["frontend-proxy", "frontend", "load-generator", "cart", "checkout", "payment", "shipping", "quote", "currency", "product-catalog", "recommendation", "ad", "email", "accounting", "fraud-detection", "kafka", "cart-cache", "checkout-db", "image-provider", "otelcol-contrib"];
const routes = [[2,0],[0,1],[1,3],[1,4],[1,8],[1,9],[1,10],[1,11],[4,3],[4,5],[4,6],[4,8],[4,9],[4,12],[4,17],[6,7],[10,9],[3,16],[5,15],[4,15],[15,13],[15,14],[2,9]];
const columns = ["kind", "service", "caller", "callee", "edge_type", "calls", "average_ms", "error_rate", "health", "p95_ms", "spans"];
const rows = [...names.map((service, i) => ["node", service, "", "", "", null, null, i === 4 ? 7 : 0, i === 4 ? "unhealthy" : "healthy", 30, 1000]), ...routes.map(([a,b], i) => ["edge", "", names[a], names[b], "call", (i+1)*100, 20, i === 0 ? 5 : i === 1 ? 1 : 0, "", null, null])];
export const demoFrame: Frame = { rows: rows.length, columns: columns.map(name => ({ name, type: "string", role: "dimension" })), values: columns.map((_, i) => rows.map(row => row[i] as string | number | null)) };
const cleanups: (() => void)[] = [];
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(fn => fn())); vi.restoreAllMocks(); document.body.innerHTML = ""; });
const model = () => serviceMapModel(demoFrame, { from_ms: 0, to_ms: 3600000 });
describe("preview V10", () => {
  it.each([{ width: 1100, height: 280 }, { width: 780, height: 220 }, { width: 270, height: 180 }])("fits twenty node boxes without overlap and preserves caller direction %o", size => {
    const graph = model(); const got = layoutServiceMap(graph, size);
    expect(got.nodes).toHaveLength(20); expect(got.edges).toHaveLength(23);
    expect(layoutServiceMap({ nodes: [...graph.nodes].reverse(), edges: [...graph.edges].reverse() }, size)).toEqual(got);
    for (const n of got.nodes) {
      expect(n.x).toBeGreaterThanOrEqual(12); expect(n.y).toBeGreaterThanOrEqual(12);
      expect(n.x+n.width).toBeLessThanOrEqual(size.width-12+.01); expect(n.y+n.height).toBeLessThanOrEqual(size.height-12+.01);
      for (const b of got.nodes.filter(b => b.id !== n.id)) expect(n.x < b.x+b.width && n.x+n.width > b.x && n.y < b.y+b.height && n.y+n.height > b.y).toBe(false);
    }
    const entry = graph.nodes.filter(n => graph.edges.some(e => e.caller === n.id) && !graph.edges.some(e => e.callee === n.id));
    for (const n of entry) for (const e of graph.edges.filter(e => e.caller === n.id)) expect(got.nodes.find(x => x.id === n.id)!.x).toBeLessThan(got.nodes.find(x => x.id === e.callee)!.x);
    for (const edge of got.edges) expect(edge.path).toMatch(/^M.*C/);
    expect(got.nodes.filter(n => n.uncalled).map(n => n.id)).toEqual(["image-provider", "otelcol-contrib"]);
    expect(got.uncalledLabel).toBeDefined();
    expect(graph.edges.find(e => e.error_rate === 5)?.status).toBe("bad"); expect(graph.edges.find(e => e.error_rate === 1)?.status).toBe("warn");
    expect(graph.nodes[0].request_rate).toBeCloseTo(1000/3600);
  });
  it.each([false, true])("dims non-neighbours, supports keyboard/filter and bounded zoom/fit (%s)", async dark => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(() => root.unmount());
    const select = vi.fn(), point = vi.fn();
    await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><PanelCard panel={{ id: "map", title: "Map", viz: "service_map", click: { set_variable: "service" } }} title="Map" result={{ id: "map", elapsed_ms: 1, status: "ok", from_ms: 0, to_ms: 3600000, frame: demoFrame }} height={300} group="g" editing={false} agentAvailable={false} loading={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} onSelect={select} onPoint={point} /></MantineProvider>));
    const button = (name: string) => host.querySelector<HTMLElement>(`[data-service-node="${name}"]`)!;
    await act(async () => button("frontend-proxy").dispatchEvent(new MouseEvent("mouseover", { bubbles: true })));
    expect(button("frontend-proxy").style.opacity).toBe("1"); expect(button("frontend").style.opacity).toBe("1"); expect(button("payment").style.opacity).toBe("0.25");
    expect(host.querySelector('[data-service-edge]')?.getAttribute("marker-end")).toBeTruthy();
    const theme = chartThemeFor(dark);
    expect(button("frontend").style.border).toContain(theme.border);
    expect(button("checkout").style.border).toContain(theme.status.bad);
    for (const edge of host.querySelectorAll('[data-service-edge]')) {
      expect(Number(edge.getAttribute("stroke-width"))).toBeGreaterThanOrEqual(1);
      expect(Number(edge.getAttribute("stroke-width"))).toBeLessThanOrEqual(4);
      expect(edge.querySelector("title")?.textContent).toContain("calls");
      expect(edge.querySelector("text")).toBeNull();
    }
    await act(async () => button("frontend").focus()); expect(document.activeElement).toBe(button("frontend"));
    expect(button("frontend").getAttribute("data-focused")).toBe("true"); expect(button("frontend").style.outline).toContain("2px");
    await act(async () => button("frontend").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(select).toHaveBeenCalledWith("frontend"); expect(point).not.toHaveBeenCalled();
    const viewport = host.querySelector('[data-service-viewport]')!;
    // Happy DOM's WheelEvent extends UIEvent and omits the browser's MouseEvent coordinates.
    const wheel = new WheelEvent("wheel", { deltaY: -500, bubbles: true, cancelable: true });
    Object.defineProperties(wheel, { clientX: { value: 250 }, clientY: { value: 94 } });
    await act(async () => viewport.dispatchEvent(wheel));
    expect(Number(viewport.getAttribute("data-zoom"))).toBeGreaterThan(1);
    await act(async () => {
      viewport.dispatchEvent(new PointerEvent("pointerdown", { button: 0, clientX: 100, clientY: 80, bubbles: true }));
      viewport.dispatchEvent(new PointerEvent("pointermove", { clientX: -10000, clientY: -10000, bubbles: true }));
      viewport.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
    });
    const zoom = Number(viewport.getAttribute("data-zoom"));
    expect(Number(viewport.getAttribute("data-pan-x"))).toBeCloseTo(500 * (1 - zoom));
    expect(Number(viewport.getAttribute("data-pan-y"))).toBeCloseTo(188 * (1 - zoom));
    const fit = host.querySelector<HTMLButtonElement>('[aria-label="Fit Map graph"]')!; expect(fit).not.toBeNull(); expect(viewport.contains(fit)).toBe(false);
    await act(async () => fit.click()); expect(viewport.getAttribute("data-zoom")).toBe("1");
    expect(host.textContent).toContain("No traced calls in this window"); expect(host.textContent).toContain("20 services · 23 routes");
  });
  it("drills without a variable and renders untrusted service names as text", async () => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(() => root.unmount()); const point = vi.fn(); const attack = '<img src=x onerror=alert(1)>';
    const frame = { ...demoFrame, values: demoFrame.values.map(values => values.map(v => v === "frontend" ? attack : v)) };
    await act(async () => root.render(<MantineProvider><ServiceMapViz panel={{ id: "m", title: "Map", viz: "service_map" }} result={{ id: "m", status: "ok", elapsed_ms: 1, frame }} dark={false} height={200} onPoint={point} /></MantineProvider>));
    const button = [...host.querySelectorAll<HTMLButtonElement>('[data-service-node]')].find(n => n.dataset.serviceNode === attack)!;
    expect(host.querySelector("img")).toBeNull(); expect(button.title).toContain(attack);
    await act(async () => button.click()); expect(point).toHaveBeenCalledWith({ dimensions: { service: attack } });
    expect(makeDrill({ id: "m", title: "Map", viz: "service_map" }, { id: "m", status: "ok", elapsed_ms: 1, from_ms: 0, to_ms: 3600000 }, point.mock.lastCall![0])).toMatchObject({ kind: "traces", dimensions: { service: attack } });
  });
});
