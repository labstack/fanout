import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { TraceDetailView, flameModel } from "./detail";
import { traceFixture } from "../../mcp-apps/fixtures";

it.each([false, true])("shares waterfall/logs/truncation and flame view without chat actions (dark=%s)", async dark => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  try {
    await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><TraceDetailView result={traceFixture} dark={dark} /></MantineProvider>));
    expect(node.textContent).toContain("correlated failure"); expect(node.textContent).toContain("1 of 3 spans");
    expect(node.querySelector('[aria-label^="cart on checkout took"]')).not.toBeNull();
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    expect(node.textContent).toContain("lanes preserve span hierarchy without overlap");
    expect(node.querySelector('[aria-label^="cart · checkout ·"]')?.hasAttribute("disabled")).toBe(true);
    expect(node.textContent).not.toMatch(/Explain|Investigate|fix it/);
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});
it("places hierarchy and overlapping siblings in separate flame lanes and tolerates cycles", () => {
  const root = traceFixture.data.spans[0];
  const model = flameModel([root, { ...root, span_id: "a", parent_span_id: "root" }, { ...root, span_id: "b", parent_span_id: "root" }]);
  expect(model.frames.map(f => f.lane)).toEqual([0, 1, 2]); expect(model.total).toBe(10);
  expect(flameModel([{ ...root, parent_span_id: "root" }]).frames).toHaveLength(1);
});
