import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { makeDrill, parseDrill } from "./drill-state";
import { router } from "../router";
import { parseSearch, toSearchParams } from "./search";
import type { Panel, PanelResult } from "../../../panels/types";
const panel: Panel = {
  id: "p",
  title: "P",
  viz: "timeseries",
  drill: "traces",
  query: { from: "spans", measures: ["count()"], by: ["service"] },
};
const result: PanelResult = {
  id: "p",
  status: "ok",
  elapsed_ms: 1,
  from_ms: 100000,
  to_ms: 500000,
  interval: "1m",
};
describe("M2 drill URL", () => {
  it("reproduces bucket, dimensions, trace and variables", () => {
    const target = makeDrill(panel, result, {
      time: 200000,
      dimensions: { service: "checkout" },
      trace_id: "abc",
      namespace: "shop",
    });
    const search = toSearchParams({
      drill: JSON.stringify(target),
      vars: { service: "checkout" },
      compare: "1",
    });
    const url = router.buildLocation({
      to: "/dashboards/$dashboardId",
      params: { dashboardId: "d" },
      search,
    });
    const decoded = parseSearch(router.options.parseSearch!(url.searchStr));
    expect(
      parseSearch(
        router.options.parseSearch!(
          router.options.stringifySearch!(toSearchParams(decoded)),
        ),
      ),
    ).toEqual(decoded);
    expect(parseDrill(decoded.drill)).toEqual(target);
    expect(decoded.vars).toEqual({ service: "checkout" });
    expect(target?.from).toBe(new Date(100000).toISOString()); // A specific trace retains the whole panel window.
  });
  it("rejects malformed, oversized and backwards payloads", () => {
    expect(parseDrill("{")).toBeUndefined();
    expect(parseDrill("x".repeat(4097))).toBeUndefined();
    expect(
      parseDrill(
        JSON.stringify({
          panel_id: "p",
          kind: "traces",
          from: "2026-10-01T13:00:00Z",
          to: "2026-10-01T12:00:00Z",
          dimensions: {},
        }),
      ),
    ).toBeUndefined();
  });
});

import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { DrillDrawer } from "./drill";
import type { DashboardSpec } from "../../../panels/types";
import type { DrillTarget } from "./drill-state";
const hostAPI = vi.hoisted(() => ({ exemplars: vi.fn(), trace: vi.fn() }));
const wire = vi.hoisted(() => ({ exemplars: vi.fn(), trace: vi.fn() }));
vi.mock("./api", () => ({
  queryExemplars: hostAPI.exemplars,
  getTrace: hostAPI.trace,
}));
const spec: DashboardSpec = {
  version: 1,
  name: "D",
  time: { range: "1h" },
  panels: [panel],
};
const target: DrillTarget = {
  panel_id: "p",
  kind: "traces",
  from: "2026-10-01T12:00:00Z",
  to: "2026-10-01T13:00:00Z",
  window_from: "2026-10-01T12:00:00Z",
  window_to: "2026-10-01T13:00:00Z",
  dimensions: { service: "checkout" },
};
const tick = () => new Promise((resolve) => setTimeout(resolve, 20));
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => {
  expect(hostAPI.exemplars).not.toHaveBeenCalled();
  expect(hostAPI.trace).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});
it("aborts an obsolete selection and keeps the next selection visible", async () => {
  let signal: AbortSignal | undefined;
  let finish: (value: { traces: [] }) => void = () => undefined;
  wire.exemplars.mockImplementation((_body: unknown, incoming: AbortSignal) => {
    signal = incoming;
    return new Promise((resolve, reject) => {
      finish = resolve;
      incoming.addEventListener(
        "abort",
        () => reject(new DOMException("Aborted", "AbortError")),
        { once: true },
      );
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const node = document.createElement("div");
  document.body.append(node);
  const root = createRoot(node);
  const change = vi.fn();
  const render = (active?: DrillTarget) => (
    <MantineProvider>
      <QueryClientProvider client={client}>
        <DrillDrawer client={wire}
          spec={spec}
          time={spec.time}
          vars={{}}
          target={active}
          onChange={change}
        />
      </QueryClientProvider>
    </MantineProvider>
  );
  await act(async () => {
    root.render(render(target));
    await tick();
  });
  expect(signal).toBeDefined();
  expect(signal?.aborted).toBe(false);
  await act(async () => {
    root.render(render());
    await tick();
  });
  expect(signal?.aborted).toBe(true);
  wire.exemplars.mockResolvedValueOnce({
    traces: [
      {
        trace_id: "next-trace",
        namespace: "shop",
        service: "next-service",
        operation: "next-operation",
        duration_ms: 1,
        status: "OK",
        start: target.from,
      },
    ],
  });
  const next = { ...target, dimensions: { service: "next-service" } };
  await act(async () => {
    root.render(render(next));
    await tick();
  });
  expect(document.body.textContent).toContain("next-operation");
  await act(async () => {
    finish({ traces: [] });
    await tick();
  });
  expect(document.body.textContent).toContain("next-operation");
  expect(document.body.textContent).not.toContain("No exemplar traces match");
  await act(async () => root.unmount());
  client.clear();
  node.remove();
});
it("renders the existing waterfall and correlated logs with visible truncation", async () => {
  wire.trace.mockResolvedValue({
    schema: "fanout.trace.v1",
    summary: "Trace",
    provenance: {
      query_id: "q",
      window: "w",
      generated_at: target.to,
      complete: true,
      data_source: "spans",
    },
    data: {
      trace_id: "abc",
      duration_ms: 10,
      has_error: true,
      services: ["checkout"],
      spans: [
        {
          span_id: "root",
          service: "checkout",
          operation: "cart",
          kind: "SPAN_KIND_SERVER",
          start: target.from,
          duration_ms: 10,
          status: "STATUS_CODE_ERROR",
        },
      ],
      logs: [
        {
          time: target.from,
          severity: "ERROR",
          service: "checkout",
          body: "correlated failure",
          trace_id: "abc",
        },
      ],
      span_count: 3,
      service_count: 2,
      truncated: true,
    },
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const node = document.createElement("div");
  document.body.append(node);
  const root = createRoot(node);
  await act(async () => {
    root.render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <DrillDrawer client={wire}
            spec={spec}
            time={spec.time}
            vars={{}}
            target={{ ...target, trace_id: "abc", namespace: "shop" }}
            onChange={() => undefined}
          />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await tick();
  });
  await act(async () => {
    await tick();
  });
  expect(document.body.textContent).toContain("correlated failure");
  expect(document.body.textContent).toContain("1 of 3 spans");
  expect(
    document.body.querySelector('[aria-label^="cart on checkout took"]'),
  ).not.toBeNull();
  const row = document.body.querySelector('[aria-label^="cart on checkout took"]')!.closest("tr")!;
  try {
    expect(row.hasAttribute("tabindex")).toBe(false);
    expect(row.style.cursor).not.toBe("pointer");
  } finally {
    await act(async () => root.unmount()); client.clear(); node.remove();
  }
});

async function mountDrawer(active: DrillTarget = target) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const node = document.createElement("div"); document.body.append(node);
  const root = createRoot(node); const change = vi.fn();
  const render = async (next?: DrillTarget) => act(async () => {
    root.render(<MantineProvider><QueryClientProvider client={client}>
      <DrillDrawer client={wire} spec={spec} time={spec.time} vars={{}} target={next} onChange={change} />
    </QueryClientProvider></MantineProvider>);
  });
  // The dashboard keeps the drawer mounted while closed before a selection opens it.
  await render(undefined);
  await render(active);
  return { change, render, async cleanup() { await act(async () => root.unmount()); client.clear(); node.remove(); } };
}

it("announces loading drill data with a status role", async () => {
  wire.exemplars.mockImplementation(() => new Promise(() => undefined));
  const drawer = await mountDrawer();
  try {
    const loader = document.body.querySelector('[aria-label="Loading drill data"]');
    expect(loader?.getAttribute("role")).toBe("status");
  } finally { await drawer.cleanup(); }
});

it("focuses the drawer, closes on Escape, and returns focus to its trigger", async () => {
  wire.exemplars.mockResolvedValue({ traces: [] });
  const trigger = document.createElement("button"); trigger.textContent = "Open traces";
  document.body.append(trigger); trigger.focus();
  const drawer = await mountDrawer();
  try {
    const dialog = document.body.querySelector('[role="dialog"]')!;
    expect(dialog).not.toBeNull();
    await act(async () => { await vi.waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true), { interval: 5, timeout: 3000 }); });
    await act(async () => { document.activeElement!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
    expect(drawer.change).toHaveBeenCalledWith(undefined);
    await drawer.render(undefined);
    await act(async () => { await vi.waitFor(() => expect(document.activeElement).toBe(trigger), { interval: 5, timeout: 3000 }); });
  } finally { await drawer.cleanup(); trigger.remove(); }
});

it("returns to exemplar traces by clearing only the trace identity", async () => {
  wire.trace.mockImplementation(() => new Promise(() => undefined));
  const drawer = await mountDrawer({ ...target, trace_id: "abc", namespace: "shop" });
  try {
    const back = [...document.body.querySelectorAll("button")].find(button => button.textContent === "Back to traces");
    expect(back).toBeDefined();
    await act(async () => back!.click());
    expect(drawer.change).toHaveBeenCalledWith(target);
    const next = drawer.change.mock.lastCall![0] as DrillTarget;
    expect(next).not.toHaveProperty("trace_id"); expect(next).not.toHaveProperty("namespace");
    wire.exemplars.mockResolvedValue({ traces: [] });
    await drawer.render(next); await act(async () => { await tick(); });
    expect(document.body.textContent).toContain("No exemplar traces match this selection.");
  } finally { await drawer.cleanup(); }
});

it.each(["traces", "logs"] as const)("shows the empty %s selection", async kind => {
  wire.exemplars.mockResolvedValue({ traces: [], logs: { columns: [], values: [], rows: 0 } });
  const original = panel.query!.from; if (kind === "logs") panel.query!.from = "logs";
  const drawer = await mountDrawer({ ...target, kind });
  try {
    await act(async () => { await tick(); });
    expect(document.body.textContent).toContain(kind === "logs" ? "No logs match this selection." : "No exemplar traces match this selection.");
  } finally { await drawer.cleanup(); panel.query!.from = original; }
});

it.each([false, true])("shows a drill request error (trace=%s)", async trace => {
  const message = trace ? "Trace unavailable" : "Exemplars unavailable";
  (trace ? wire.trace : wire.exemplars).mockRejectedValue(new Error(message));
  const drawer = await mountDrawer(trace ? { ...target, trace_id: "abc", namespace: "shop" } : target);
  try {
    await act(async () => { await tick(); });
    const alert = document.body.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain(message);
    expect(document.body.textContent).not.toContain("No exemplar traces match");
  } finally { await drawer.cleanup(); }
});

it("shows an empty trace and its empty correlated logs", async () => {
  wire.trace.mockResolvedValue({ data: { trace_id: "empty", spans: [], logs: [], has_error: false } });
  const drawer = await mountDrawer({ ...target, trace_id: "empty", namespace: "shop" });
  try {
    await act(async () => { await tick(); });
    expect(document.body.textContent).toContain("No spans were found for this trace.");
    expect(document.body.textContent).toContain("No correlated logs");
  } finally { await drawer.cleanup(); }
});

import { TableViz } from "./viz/table";
it("drills configured trace-link rows with their namespace after sorting, without making empty rows actionable", async () => {
  const node = document.createElement("div");
  document.body.append(node);
  const root = createRoot(node);
  const onPoint = vi.fn();
  const linked: Panel = {
    id: "p",
    title: "Links",
    viz: "table",
    sql: "SELECT trace_id AS trace FROM spans",
    options: { columns: [{ field: "trace", format: "trace_link" }] },
  };
  const data: PanelResult = {
    ...result,
    frame: {
      columns: [
        { name: "trace", type: "string", role: "dimension" },
        { name: "namespace", type: "string", role: "dimension" },
        { name: "count", type: "number", role: "measure" },
      ],
      values: [
        ["abc", ""],
        ["shop", "ops"],
        [3, 1],
      ],
      rows: 2,
    },
  };
  try {
    await act(async () =>
      root.render(
        <MantineProvider>
          <TableViz
            panel={linked}
            result={data}
            height={200}
            onPoint={onPoint}
          />
        </MantineProvider>,
      ),
    );
    const initial = [...node.querySelectorAll<HTMLTableRowElement>("tbody tr")];
    expect(initial[0].style.cursor).toBe("pointer");
    expect(initial[1].style.cursor).toBe("");
    expect(initial[1].hasAttribute("tabindex")).toBe(false);
    await act(async () => initial[1].click());
    expect(onPoint).not.toHaveBeenCalled();
    await act(async () =>
      node.querySelector<HTMLButtonElement>("th:nth-child(3) button")!.click(),
    );
    await act(async () =>
      node.querySelector<HTMLButtonElement>("th:nth-child(3) button")!.click(),
    );
    const sorted = [...node.querySelectorAll<HTMLTableRowElement>("tbody tr")];
    expect(sorted[0].textContent).toContain("ops");
    await act(async () =>
      sorted[1].dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      ),
    );
    expect(onPoint).toHaveBeenCalledOnce();
    expect(onPoint).toHaveBeenCalledWith({
      time: undefined,
      dimensions: {},
      trace_id: "abc",
      namespace: "shop",
    });
    await act(async () =>
      root.render(
        <MantineProvider>
          <TableViz
            panel={{ ...linked, options: undefined }}
            result={data}
            height={200}
            onPoint={onPoint}
          />
        </MantineProvider>,
      ),
    );
    expect(
      [...node.querySelectorAll<HTMLTableRowElement>("tbody tr")].every(
        (row) => row.style.cursor === "" && !row.hasAttribute("tabindex"),
      ),
    ).toBe(true);
  } finally {
    await act(async () => root.unmount());
    node.remove();
  }
});

it("sends the narrowed deploy selection to the exemplar endpoint", async () => {
 wire.exemplars.mockResolvedValue({traces:[]});
 const before = {...target,to:"2026-10-01T12:30:00Z",dimensions:{service:"checkout",operation:"PlaceOrder"}};
 const drawer=await mountDrawer(before);
 try {
  await act(async()=>{await tick();});
  expect(wire.exemplars.mock.lastCall?.[0]).toMatchObject({from:before.from,to:before.to,dimensions:before.dimensions,time:{from:target.window_from,to:target.window_to}});
 }finally{await drawer.cleanup();}
});
