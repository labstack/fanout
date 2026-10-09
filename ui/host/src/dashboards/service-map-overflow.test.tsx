import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { Frame } from "../../../panels/types";
import { wallFrame } from "../../tests/service-map-wall";
import { PanelCard } from "./panel-card";

const size = { width: 554, height: 420 };
let cleanup = () => {};
afterEach(async () => {
  await act(async () => cleanup());
  cleanup = () => {};
  vi.restoreAllMocks(); vi.unstubAllGlobals();
});
async function renderMap(frame: Frame, dark = false) {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => DOMRect.fromRect(size));
  const host = document.createElement("div"), root = createRoot(host);
  cleanup = () => root.unmount();
  await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><PanelCard panel={{ id: "m", title: "Map", viz: "service_map" }} title="Map" result={{ id: "m", status: "ok", elapsed_ms: 0, frame }} loading={false} height={508} group="g" editing={false} agentAvailable={false} /></MantineProvider>));
  return host;
}
it.each([false, true])("fades the overflowing right wall edge without intercepting map interaction (dark=%s)", async dark => {
  const host = await renderMap(wallFrame, dark);
  const viewport = host.querySelector<HTMLElement>("[data-service-viewport]")!;
  expect(viewport.getAttribute("data-map-overflow")).toBe("right");
  expect(viewport.getAttribute("data-map-fade")).toBe("right");
  const fade = viewport.querySelector<HTMLElement>('[aria-hidden="true"][style*="linear-gradient"]')!;
  expect(fade).not.toBeNull();
  expect(fade.style.right).toBe("0px");
  expect(fade.style.background).toContain("var(--mantine-color-body)");
  expect(fade.style.pointerEvents).toBe("none");
});
it("keeps a fitting map free of fades and Fit controls", async () => {
  const frame = { ...wallFrame, rows: 1, values: wallFrame.values.map(column => column.slice(0, 1)) };
  const host = await renderMap(frame);
  const viewport = host.querySelector<HTMLElement>("[data-service-viewport]")!;
  expect(viewport.getAttribute("data-map-overflow")).toBe("");
  expect(viewport.getAttribute("data-map-fade")).toBe("");
  expect(viewport.querySelector('[style*="linear-gradient"]')).toBeNull();
  expect(host.querySelector('[aria-label*="whole"]')).toBeNull();
  expect(host.querySelector('[aria-label*="readable"]')).toBeNull();
});
it.each([false, true])("offers whole-wall Fit immediately and toggles back to the readable entry view (dark=%s)", async dark => {
  const host = await renderMap(wallFrame, dark);
  const viewport = host.querySelector<HTMLElement>("[data-service-viewport]")!;
  const content = viewport.querySelector<HTMLElement>("[data-service-content]")!;
  const initial = content.style.transform;
  const fit = host.querySelector<HTMLButtonElement>('[aria-label="Show whole Map graph"]');
  expect(fit).not.toBeNull();
  await act(async () => fit!.click());
  expect(fit!.getAttribute("aria-label")).toBe("Return to readable Map view");
  const transform = content.style.transform.match(/translate\(([-\d.]+)px, ([-\d.]+)px\) scale\(([\d.]+)\)/)!;
  const [, x, y, zoom] = transform.map(Number);
  expect(zoom).toBeLessThan(1);
  const nodes = [...content.querySelectorAll<HTMLElement>("[data-service-node]")];
  expect(nodes).toHaveLength(18);
  for (const node of nodes) {
    expect(parseFloat(node.style.left) * zoom + x).toBeGreaterThanOrEqual(0);
    expect(parseFloat(node.style.top) * zoom + y).toBeGreaterThanOrEqual(0);
    expect((parseFloat(node.style.left) + parseFloat(node.style.width)) * zoom + x).toBeLessThanOrEqual(size.width);
    expect((parseFloat(node.style.top) + parseFloat(node.style.height)) * zoom + y).toBeLessThanOrEqual(size.height);
  }
  expect(viewport.getAttribute("data-map-overflow")).toBe("");
  expect(viewport.getAttribute("data-map-fade")).toBe("");
  expect(viewport.querySelector('[style*="linear-gradient"]')).toBeNull();
  await act(async () => fit!.click());
  expect(content.style.transform).toBe(initial);
  expect(fit!.getAttribute("aria-label")).toBe("Show whole Map graph");
  expect(viewport.getAttribute("data-map-fade")).toBe("right");
  for (const node of nodes) expect(parseFloat(node.querySelector<HTMLElement>("[data-service-text]")!.style.fontSize)).toBeGreaterThanOrEqual(11);
});
it("updates fades for each edge as the map is dragged, clearing them when it fits again", async () => {
  const frame = { ...wallFrame, rows: 1, values: wallFrame.values.map(column => column.slice(0, 1)) };
  const host = await renderMap(frame);
  const viewport = host.querySelector<HTMLElement>("[data-service-viewport]")!;
  for (const [x, y, edges] of [[-1000, -1000, "left top"], [2000, 2000, "right bottom"], [-1000, -1000, ""]] as const) {
    await act(async () => {
      viewport.dispatchEvent(new PointerEvent("pointerdown", { button: 0, clientX: 0, clientY: 0, bubbles: true }));
      viewport.dispatchEvent(new PointerEvent("pointermove", { clientX: x, clientY: y, bubbles: true }));
      viewport.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
    });
    expect(viewport.getAttribute("data-map-overflow")).toBe(edges);
    expect(viewport.getAttribute("data-map-fade")).toBe(edges);
    expect(viewport.querySelectorAll('[style*="linear-gradient"]')).toHaveLength(edges ? 2 : 0);
  }
});
