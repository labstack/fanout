import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { p95, assertRefreshRequests, performanceReadinessCause, allPanelsPainted } from "./performance-support";

it("uses nearest-rank p95 without dropping slow samples or mutating input", () => {
  const samples = Array.from({ length: 20 }, (_, i) => 20 - i);
  expect(p95(samples)).toBe(19);
  expect(samples[0]).toBe(20);
  expect(p95([...Array(19).fill(1), 10000])).toBe(1);
  expect(p95([...Array(18).fill(1), 9000, 10000])).toBe(9000);
  for (const invalid of [[1, 2], [...Array(19).fill(1), NaN], [...Array(19).fill(1), Infinity], [...Array(19).fill(1), -1]]) {
    expect(() => p95(invalid)).toThrow();
  }
});

it("counts completed refresh requests including duplicates and variable reloads", () => {
  const valid = ["/api/panels/query", "/api/annotations"];
  expect(() => assertRefreshRequests(valid)).not.toThrow();
  for (const extra of ["/api/panels/query", "/api/annotations", "/api/panels/variables/resolve"]) {
    expect(() => assertRefreshRequests([...valid, extra])).toThrow();
  }
  expect(() => assertRefreshRequests(valid.slice(0, 1))).toThrow();
  expect(() => assertRefreshRequests(valid.slice(1))).toThrow();
});

it("waits for exact seeded totals and twelve unique populated terminal results", () => {
  const ids = ["count", "logs", ...Array.from({ length: 10 }, (_, i) => `p${i}`)];
  const results = ids.map(id => ({ id, status: "ok", frame: { rows: 48, values: [[180]], totals: [null, 8640] } }));
  expect(performanceReadinessCause({ results }, ids)).toBeUndefined();
  expect(performanceReadinessCause({ results: results.slice(1) }, ids)).toContain("Missing");
  expect(performanceReadinessCause({ results: [...results.slice(1), results[1]] }, ids)).toContain("duplicate");
  expect(performanceReadinessCause({ results: results.map(r => r.id === "count" ? { ...r, frame: { ...r.frame, totals: [null, 8634] } } : r) }, ids)).toContain("8640");
  expect(performanceReadinessCause({ results: results.map(r => r.id === "p1" ? { ...r, status: "error" } : r) }, ids)).toContain("p1");
  expect(performanceReadinessCause({ results: results.map(r => r.id === "p1" ? { ...r, frame: { ...r.frame, rows: 0 } } : r) }, ids)).toContain("p1");
});

describe("in-page paint completion", () => {
  const panels = [{ id: "stat", viz: "stat" }, { id: "table", viz: "table" }, { id: "chart", viz: "timeseries" }];
  const state = window as typeof window & { __fanoutPerformanceReadyFrames?: number };
  const fontsDescriptor = Object.getOwnPropertyDescriptor(document, "fonts");
  let alpha: number, clipped: boolean, transparent: boolean;
  beforeEach(() => {
    delete state.__fanoutPerformanceReadyFrames;
    alpha = 255; clipped = transparent = false;
    Object.defineProperty(document, "fonts", { configurable: true, value: { status: "loaded" } });
    vi.stubGlobal("innerWidth", 1440); vi.stubGlobal("innerHeight", 3000);
    vi.stubGlobal("getComputedStyle", (node: HTMLElement) => ({ display: "block", visibility: "visible", opacity: transparent && node.hasAttribute("data-panel") ? "0" : "1", overflowX: "hidden", overflowY: "hidden", contentVisibility: "visible" }));
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      const right = clipped && this.tagName === "CANVAS" ? 600 : 500;
      return { x: 0, y: 0, left: 0, top: 0, right, bottom: 100, width: right, height: 100, toJSON: () => ({}) };
    });
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => ({
      drawImage: () => undefined, getImageData: () => ({ data: new Uint8ClampedArray([0, 0, 0, alpha]) }),
    }) as unknown as CanvasRenderingContext2D);
    document.body.innerHTML = '<div class="mantine-Paper-root" data-panel="stat"><span data-stat-value>8640</span></div><div class="mantine-Paper-root" data-panel="table"><table><tbody><tr><td>1440</td></tr></tbody></table></div><div class="mantine-Paper-root" data-panel="chart"><canvas width="500" height="100"></canvas></div>';
  });
  afterEach(() => {
    vi.restoreAllMocks(); vi.unstubAllGlobals();
    if (fontsDescriptor) Object.defineProperty(document, "fonts", fontsDescriptor);
    else Reflect.deleteProperty(document, "fonts");
    delete state.__fanoutPerformanceReadyFrames;
    document.body.innerHTML = "";
  });
  it("returns an in-page timestamp only after two consecutive ready frames", () => {
    expect(allPanelsPainted(panels)).toBe(false);
    expect(allPanelsPainted(panels)).toEqual(expect.any(Number));
    document.querySelector("[data-stat-value]")!.remove();
    expect(allPanelsPainted(panels)).toBe(false);
    expect(state.__fanoutPerformanceReadyFrames).toBe(0);
  });
  it("rejects blank canvases, clipping, transparent ancestors, loaders and errors", () => {
    alpha = 0; expect(allPanelsPainted(panels)).toBe(false);
    alpha = 255; clipped = true; expect(allPanelsPainted(panels)).toBe(false);
    clipped = false; transparent = true; expect(allPanelsPainted(panels)).toBe(false);
    transparent = false;
    const card = document.querySelector('[data-panel="stat"]')!;
    for (const marker of ['aria-label="Loading panel"', 'aria-label="Refreshing"', "data-panel-error", "data-refresh-error-message"]) {
      card.insertAdjacentHTML("beforeend", `<span ${marker}></span>`);
      expect(allPanelsPainted(panels)).toBe(false);
      card.lastChild!.remove();
    }
    Object.defineProperty(document, "fonts", { configurable: true, value: { status: "loading" } });
    expect(allPanelsPainted(panels)).toBe(false);
  });
});
