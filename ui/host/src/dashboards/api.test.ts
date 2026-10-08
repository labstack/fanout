import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DrillTarget } from "./drill-state";
import type { DashboardSpec } from "../../../panels/types";
import { ApiError, getTrace, queryExemplars, queryAnnotations, getDashboard, queryPanels, patchDashboard, replaceDashboard } from "./api";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("../auth", () => ({ authorizedFetch: fetchMock }));


const spec = { version: 1, name: "n", time: {}, panels: [] } as DashboardSpec;
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const lastCall = () => fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit];

describe("dashboard api", () => {
  beforeEach(() => fetchMock.mockReset());

  it("encodes dashboard ids when reading the active record",async()=>{
    fetchMock.mockResolvedValue(json(200,{id:"a b"}));await expect(getDashboard("a b")).resolves.toEqual({id:"a b"});expect(lastCall()[0]).toBe("/api/dashboards/a%20b");
  });
  it("sends base_version and message on PUT and PATCH", async () => {
    fetchMock.mockResolvedValue(json(200, {}));
    await replaceDashboard("d1", spec, 4, "tweak");
    expect(lastCall()[1].method).toBe("PUT");
    expect(JSON.parse(lastCall()[1].body as string)).toEqual({ spec, base_version: 4, message: "tweak" });
    await patchDashboard("d1", [{ op: "remove_panel", id: "p" }], 5, "drop");
    expect(lastCall()[1].method).toBe("PATCH");
    expect(JSON.parse(lastCall()[1].body as string)).toEqual({ operations: [{ op: "remove_panel", id: "p" }], base_version: 5, message: "drop" });
    expect(new Headers(lastCall()[1].headers).get("content-type")).toBe("application/json");
    expect((lastCall()[1] as { json?: unknown }).json).toBeUndefined();
  });

  it("turns a 400 with problems into an ApiError", async () => {
    const problems = [{ path: "panels[0].id", message: "bad", hint: "use lowercase" }];
    fetchMock.mockResolvedValue(json(400, { message: "The dashboard spec is invalid.", problems }));
    const error = await replaceDashboard("d1", spec, 1).catch((e) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error.status).toBe(400);
    expect(error.problems).toEqual(problems);
    expect(error.message).toBe("The dashboard spec is invalid.");
  });

  it("posts panel batches using the shared JSON request path",async()=>{
    fetchMock.mockResolvedValue(json(200,{results:[]}));await expect(queryPanels({dashboard:spec})).resolves.toEqual([]);expect(lastCall()[0]).toBe("/api/panels/query");expect(lastCall()[1].method).toBe("POST");expect(JSON.parse(String(lastCall()[1].body))).toEqual({dashboard:spec});
  });
  it("posts annotation windows as JSON and forwards the abort signal", async () => {
    const annotations = { deploys: [], anomalies: [], truncated: true };
    fetchMock.mockResolvedValue(json(200, annotations));
    const body = { from: "2026-10-01T12:00:00Z", to: "2026-10-01T13:00:00Z" };
    const signal = new AbortController().signal;
    await expect(queryAnnotations(body, signal)).resolves.toEqual(annotations);
    const [url, init] = lastCall();
    expect(url).toBe("/api/annotations"); expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual(body); expect(init.signal).toBe(signal);
    expect(new Headers(init.headers).get("content-type")).toBe("application/json");
  });
});


it("uses the captured full window for trace lookups and passes cancellation to both drill requests", async () => {
  fetchMock.mockResolvedValue(json(200, {}));
  const signal = new AbortController().signal;
  const target: DrillTarget = { panel_id: "p", kind: "traces", from: "2026-10-01T12:30:00Z", to: "2026-10-01T12:31:00Z", window_from: "2026-10-01T12:00:00Z", window_to: "2026-10-01T13:00:00Z", trace_id: "a&b", namespace: "shop & ops", dimensions: { service: "checkout" }, bucket: { lower: 1, upper: 2 } };
  await getTrace(target, signal);
  const url = new URL(lastCall()[0], "http://localhost");
  expect(url.pathname).toBe("/api/traces/a%26b");
  expect(Object.fromEntries(url.searchParams)).toEqual({ namespace: "shop & ops", from: target.window_from, to: target.window_to, limit: "200" });
  expect(lastCall()[1].signal).toBe(signal);
  const body = { dashboard: spec, panel_id: target.panel_id, kind: "traces" as const, from: target.from, to: target.to, time: { from: target.window_from, to: target.window_to, refresh: "off" }, dimensions: target.dimensions, bucket: target.bucket };
  await queryExemplars(body, signal);
  expect(lastCall()[0]).toBe("/api/panels/exemplars");
  expect(lastCall()[1].method).toBe("POST");
  expect(lastCall()[1].signal).toBe(signal);
  expect(JSON.parse(String(lastCall()[1].body))).toEqual(body);
});
