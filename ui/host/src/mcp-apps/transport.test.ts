import type { App } from "@modelcontextprotocol/ext-apps";
import { expect, it, vi } from "vitest";
import { appTransport } from "./transport";
import { fixture, traceFixture } from "./fixtures";
import type { QueryBody } from "../dashboards/api";

it("queries a complete fragment with scalar, multi, All and empty values without fetch", async () => {
  const fetch = vi.spyOn(globalThis, "fetch");
  const callServerTool = vi.fn().mockResolvedValue({ structuredContent: fixture() });
  const transport = appTransport({ callServerTool } as unknown as App);
  const vars = { scalar: "checkout", multi: ["cart", "checkout"], all: "$__all", empty: [] };
  try {
    const result = await transport.query({ dashboard: fixture().dashboard, vars });
    expect(result.vars).toEqual(vars);
    expect(callServerTool).toHaveBeenCalledExactlyOnceWith({ name: "query_panel_fragment", arguments: { dashboard: fixture().dashboard, vars } }, { signal: undefined });
    expect(fetch).not.toHaveBeenCalled();
  } finally { fetch.mockRestore(); }
});
it.each([[], null, undefined])("rejects a raw panels key (%s) before calling the bridge", async panels => {
  const callServerTool = vi.fn();
  const transport = appTransport({ callServerTool } as unknown as App);
  await expect(transport.query({ dashboard: fixture().dashboard, panels } as QueryBody)).rejects.toThrow("Panel subsets are unavailable in chat fragments");
  expect(callServerTool).not.toHaveBeenCalled();
});
it("passes exact nanosecond trace scope and cancellation in SDK RequestOptions", async () => {
  const callServerTool = vi.fn().mockResolvedValue({ structuredContent: { ...fixture(), trace: traceFixture } });
  const transport = appTransport({ callServerTool } as unknown as App);
  const signal = new AbortController().signal;
  const target = { panel_id: "p", kind: "traces" as const, dimensions: {}, trace_id: "abc", namespace: "shop", from: "2026-10-08T00:00:00.000000001Z", to: "2026-10-08T01:00:00.000000002Z", window_from: "2026-10-08T00:00:00.000000001Z", window_to: "2026-10-08T01:00:00.000000002Z" };
  expect(await transport.drill.trace(target, signal)).toEqual(traceFixture);
  expect(callServerTool).toHaveBeenCalledWith({ name: "inspect_trace", arguments: { trace_id: "abc", namespace: "shop", from: target.window_from, to: target.window_to, limit: 200 } }, { signal });
  const controller = new AbortController();
  callServerTool.mockImplementation(async () => { controller.abort(); return { structuredContent: { ...fixture(), trace: traceFixture } }; });
  await expect(transport.drill.trace(target, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  const calls = callServerTool.mock.calls.length;
  await expect(transport.drill.trace(target, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  expect(callServerTool).toHaveBeenCalledTimes(calls);
});
it.each([{ isError: true }, {}, { structuredContent: { data: {} } }])("rejects errors, absent content and old payloads", async result => {
  const callServerTool = vi.fn().mockResolvedValue(result);
  await expect(appTransport({ callServerTool } as unknown as App).query({ dashboard: fixture().dashboard })).rejects.toThrow();
});
it("resolves options and exemplars only through the bridge", async () => {
  const callServerTool = vi.fn().mockResolvedValueOnce({ structuredContent: { options: { service: [{ value: "checkout" }] } } }).mockResolvedValueOnce({ structuredContent: { traces: [] } });
  const transport = appTransport({ callServerTool } as unknown as App);
  expect(await transport.resolveVariables({ dashboard: fixture().dashboard })).toEqual({ service: [{ value: "checkout" }] });
  const controller = new AbortController();
  const body = { dashboard: fixture().dashboard, panel_id: "p", from: "a", to: "b", vars: { service: [] } };
  await transport.drill.exemplars(body, controller.signal);
  expect(callServerTool.mock.calls[0][0].name).toBe("resolve_panel_variables");
  expect(callServerTool).toHaveBeenLastCalledWith({ name: "get_panel_exemplars", arguments: body }, { signal: controller.signal });
});
