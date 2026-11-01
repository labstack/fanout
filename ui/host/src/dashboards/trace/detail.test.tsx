import { MantineProvider } from "@mantine/core";
import { within } from "@testing-library/dom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TraceDetailView } from "./detail";
import { Waterfall } from "./waterfall";
import { traceFixture } from "../../../tests/fixtures";
import { formatTimestamp } from "../../../../panels/units";
import { duration } from "../../../../format";

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

it.each([false, true].flatMap(dark => [false, true].map(external => ({dark, external}))))("shares accessible waterfall/logs/truncation and flame view (dark=$dark, external=$external)", async ({dark, external}) => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  try {
    const onSpan = external ? vi.fn() : undefined;
    await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><QueryClientProvider client={new QueryClient()}><TraceDetailView result={traceFixture} dark={dark} onSpan={onSpan} /></QueryClientProvider></MantineProvider>));
    expect(node.textContent).toContain("correlated failure"); expect(node.textContent).toContain("1 of 3 spans");
    expect(node.querySelector('[aria-label^="cart on checkout took"]')).not.toBeNull();
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    expect(node.textContent).toContain("lanes preserve span hierarchy without overlap");
    const span = traceFixture.data.spans[0];
    const frame = within(node).getByRole("button", {name: `${span.operation} · ${duration(span.duration_ms)} · ${span.service}`});
    expect(frame.getAttribute("aria-label")!.startsWith(frame.textContent!)).toBe(true);
    await act(async () => frame.click());
    // Without a caller's handler a span opens its details below the chart.
    if (onSpan) { expect(onSpan).toHaveBeenCalledWith(span); expect(node.querySelector("[data-span-details]")).toBeNull(); }
    else expect(node.querySelector("[data-span-details]")?.textContent).toContain("Error");
    expect([...node.querySelectorAll("button")].find(b => b.textContent === "Flame graph")?.getAttribute("aria-pressed")).toBe("true");
    expect(node.textContent).not.toMatch(/Explain|Investigate|fix it/);
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});
it("renders overlapping siblings on separate flame lanes and tolerates cycles", async () => {
  const span = traceFixture.data.spans[0];
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const result = { ...traceFixture, data: { ...traceFixture.data, spans: [span, { ...span, span_id: "a", parent_span_id: "root", operation: "child-a" }, { ...span, span_id: "b", parent_span_id: "root", operation: "child-b" }] } };
  const view = (value: typeof result) => <MantineProvider><QueryClientProvider client={new QueryClient()}><TraceDetailView result={value} dark={false} /></QueryClientProvider></MantineProvider>;
  try {
    await act(async () => root.render(view(result)));
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    expect([...node.querySelectorAll<HTMLElement>('button[aria-label*=" · "]')].map(frame => frame.style.top)).toEqual(["6px", "42px", "78px"]);
    await act(async () => root.render(view({ ...result, data: { ...result.data, spans: [{ ...span, parent_span_id: "root" }] } })));
    expect(node.querySelectorAll('button[aria-label*=" · "]')).toHaveLength(1);
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});

it("shows a compact flame frame tooltip on keyboard focus", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  const span = traceFixture.data.spans[0];
  const result = { ...traceFixture, data: { ...traceFixture.data, spans: [{ ...span, duration_ms: 100 }, { ...span, span_id: "compact", parent_span_id: "root", operation: "tiny", duration_ms: 1 }] } };
  try {
    await act(async () => root.render(<MantineProvider><QueryClientProvider client={new QueryClient()}><TraceDetailView result={result} dark={false} /></QueryClientProvider></MantineProvider>));
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    const frame = node.querySelector<HTMLElement>('button[aria-label^="tiny ·"]')!;
    expect(frame.textContent).toBe("");
    await act(async () => { frame.focus(); await new Promise(resolve => setTimeout(resolve, 20)); });
    expect(document.activeElement).toBe(frame);
    expect(document.body.querySelector('[role="tooltip"]')?.textContent).toContain("checkout · tiny");
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});

it("lists every span of a large trace in one list, children under their parents and indented", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  const base = traceFixture.data.spans[0];
  const at = (ms: number) => new Date(Date.parse(base.start) + ms).toISOString();
  // 300 spans: a root, then children listed before their parents and out of start order.
  const spans = [...Array.from({ length: 299 }, (_, i) => ({ ...base, span_id: `s${299 - i}`, parent_span_id: 299 - i === 1 ? "root" : `s${Math.floor((299 - i) / 2) || 1}`, operation: `op-${299 - i}`, start: at(299 - i), duration_ms: 1 })), { ...base, duration_ms: 400 }];
  try {
    await act(async () => root.render(<MantineProvider><Waterfall spans={spans} dark={false} /></MantineProvider>));
    const rows = [...node.querySelectorAll<HTMLElement>("tbody tr")];
    expect(rows).toHaveLength(300);
    expect(node.textContent).not.toMatch(/Next page|of 300/);
    expect(rows.slice(0, 4).map(row => [row.querySelector("p")?.textContent, row.dataset.depth])).toEqual([["cart", "0"], ["op-1", "1"], ["op-2", "2"], ["op-4", "3"]]);
    const parentOf = new Map(spans.map(span => [span.operation, span.parent_span_id]));
    const seen = new Set<string | undefined>(["root"]);
    for (const row of rows) { const operation = row.querySelector("p")!.textContent!; expect(seen.has(parentOf.get(operation) ?? "root")).toBe(true); seen.add(spans.find(span => span.operation === operation)!.span_id); }
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});

it("sizes the flame graph to its container instead of a fixed minimum width", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  try {
    await act(async () => root.render(<MantineProvider><QueryClientProvider client={new QueryClient()}><TraceDetailView result={traceFixture} dark={false} /></QueryClientProvider></MantineProvider>));
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    // A fixed minimum (760 px) overflowed the 682 px drawer at 1440 px and forced a horizontal scroll.
    expect(node.querySelector("[data-flame-graph]")).not.toBeNull();
    expect(node.querySelector("[data-flame-graph] .mantine-ScrollArea-root")).toBeNull();
    expect([...node.querySelectorAll<HTMLElement>("[data-flame-graph] *")].some(el => el.style.minWidth.includes("rem"))).toBe(false);
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});

it("keeps flame ticks on one line and folds services past the palette into one legend entry", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  const [first] = traceFixture.data.spans;
  const spans = Array.from({ length: 8 }, (_, i) => ({ ...first, span_id: `s${i}`, parent_span_id: i ? "s0" : undefined, service: `svc${i}`, duration_ms: i ? 1000 : 3_020_000 }));
  try {
    await act(async () => root.render(<MantineProvider><QueryClientProvider client={new QueryClient()}><TraceDetailView result={{ ...traceFixture, data: { ...traceFixture.data, spans } }} dark={false} /></QueryClientProvider></MantineProvider>));
    await act(async () => [...node.querySelectorAll<HTMLButtonElement>("button")].find(b => b.textContent === "Flame graph")!.click());
    const ticks = [...node.querySelectorAll<HTMLElement>("[data-flame-graph] > div:first-child > *")];
    expect(ticks.at(-1)?.textContent).toBe("50m 20s");
    expect(ticks.every(tick => tick.style.whiteSpace === "nowrap")).toBe(true);
    const legend = [...node.querySelectorAll("[data-flame-legend] > *")].map(item => item.textContent);
    expect(legend).toEqual(["svc0", "svc1", "svc2", "svc3", "svc4", "svc5", "2 other services: svc6, svc7"]);
    // The folded entry is reachable by keyboard and shows the names on focus.
    const others = node.querySelector<HTMLElement>("[data-flame-others]")!;
    expect(others.tabIndex).toBe(0);
    await act(async () => others.focus());
    await vi.waitFor(() => expect(document.querySelector("[role=tooltip]")?.textContent).toBe("svc6, svc7"));
  } finally { await act(async () => root.unmount()); node.remove(); vi.unstubAllGlobals(); }
});
