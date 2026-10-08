import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { TraceDetailView } from "./detail";
import { traceFixture } from "../../../tests/fixtures";
import { formatTimestamp } from "../../../../panels/units";

it("uses the dashboard logs table for correlated logs with compact times and complete badges", async () => {
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  const log = { ...traceFixture.data.logs[0], severity: "ERROR", service: "frontend-proxy" };
  try {
    await act(async () => root.render(<MantineProvider><TraceDetailView result={{ ...traceFixture, data: { ...traceFixture.data, logs: [log] } }} dark={false} /></MantineProvider>));
    const table = node.querySelector(".dashboard-table")!;
    expect(table).not.toBeNull();
    expect(table.querySelector('td[data-field="time"]')?.textContent).toBe(formatTimestamp(Date.parse(log.time)));
    expect(table.querySelector('td[data-field="severity"]')?.textContent).toContain("◆ ERROR");
    expect(table.querySelector<HTMLElement>('td[data-field="service"] > *')?.style.whiteSpace).toBe("nowrap");
  } finally { await act(async () => root.unmount()); node.remove(); }
});

it.each([false, true])("shares waterfall/logs/truncation and flame view without chat actions (dark=%s)", async dark => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  try {
    await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><TraceDetailView result={traceFixture} dark={dark} /></MantineProvider>));
    expect(node.textContent).toContain("correlated failure"); expect(node.textContent).toContain("1 of 3 spans");
    expect(node.querySelector('[aria-label^="cart on checkout took"]')).not.toBeNull();
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    expect(node.textContent).toContain("lanes preserve span hierarchy without overlap");
    expect(node.querySelector('[aria-label^="cart · checkout ·"]')?.getAttribute("role")).toBe("img");
    expect(node.querySelector('[aria-label^="cart · checkout ·"]')?.getAttribute("tabindex")).toBe("0");
    expect([...node.querySelectorAll("button")].find(b => b.textContent === "Flame graph")?.getAttribute("aria-pressed")).toBe("true");
    expect(node.textContent).not.toMatch(/Explain|Investigate|fix it/);
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});
it("renders overlapping siblings on separate flame lanes and tolerates cycles", async () => {
  const span = traceFixture.data.spans[0];
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const result = { ...traceFixture, data: { ...traceFixture.data, spans: [span, { ...span, span_id: "a", parent_span_id: "root", operation: "child-a" }, { ...span, span_id: "b", parent_span_id: "root", operation: "child-b" }] } };
  try {
    await act(async () => root.render(<MantineProvider><TraceDetailView result={result} dark={false} /></MantineProvider>));
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    expect([...node.querySelectorAll<HTMLElement>('[role="img"][tabindex="0"]')].map(frame => frame.style.top)).toEqual(["6px", "42px", "78px"]);
    await act(async () => root.render(<MantineProvider><TraceDetailView result={{ ...result, data: { ...result.data, spans: [{ ...span, parent_span_id: "root" }] } }} dark={false} /></MantineProvider>));
    expect(node.querySelectorAll('[role="img"][tabindex="0"]')).toHaveLength(1);
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});

it("shows a compact flame frame tooltip on keyboard focus", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  const span = traceFixture.data.spans[0];
  const result = { ...traceFixture, data: { ...traceFixture.data, spans: [{ ...span, duration_ms: 100 }, { ...span, span_id: "compact", parent_span_id: "root", operation: "tiny", duration_ms: 1 }] } };
  try {
    await act(async () => root.render(<MantineProvider><TraceDetailView result={result} dark={false} /></MantineProvider>));
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    const frame = node.querySelector<HTMLElement>('[role="img"][aria-label^="tiny · checkout"]')!;
    expect(frame.textContent).toBe("");
    await act(async () => { frame.focus(); await new Promise(resolve => setTimeout(resolve, 20)); });
    expect(document.activeElement).toBe(frame);
    expect(document.body.querySelector('[role="tooltip"]')?.textContent).toContain("checkout · tiny");
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});
