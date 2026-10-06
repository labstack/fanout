import { beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.hoisted(() => vi.fn());
vi.mock("../auth", () => ({ authorizedFetch: fetchMock }));

import type { DrillTarget } from "./drill-state";
import type { DashboardSpec } from "../../../panels/types";
import { ApiError, getTrace, queryExemplars, deleteDashboard, patchDashboard, replaceDashboard, restoreVersion } from "./api";

const spec = { version: 1, name: "n", time: {}, panels: [] } as DashboardSpec;
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const lastCall = () => fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit];

describe("dashboard api", () => {
  beforeEach(() => fetchMock.mockReset());

  it("confirms deletes with the id header and resolves on 204", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(deleteDashboard("a b")).resolves.toBeUndefined();
    const [url, init] = lastCall();
    expect(url).toBe("/api/dashboards/a%20b");
    expect(init.method).toBe("DELETE");
    expect(new Headers(init.headers).get("Fanout-Confirm-Delete")).toBe("a b");
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

  it("posts restores without a body", async () => {
    fetchMock.mockResolvedValue(json(200, { id: "d1" }));
    await restoreVersion("d1", 3);
    expect(lastCall()[0]).toBe("/api/dashboards/d1/versions/3/restore");
    expect(lastCall()[1].method).toBe("POST");
  });
});


it("uses the captured full window for trace lookups and passes cancellation to both drill requests", async () => {
  fetchMock.mockResolvedValue(json(200, {}));
  const signal = new AbortController().signal;
  const target: DrillTarget = { panel_id: "p", kind: "traces", from: "2026-10-01T12:30:00Z", to: "2026-10-01T12:31:00Z", window_from: "2026-10-01T12:00:00Z", window_to: "2026-10-01T13:00:00Z", trace_id: "a&b", namespace: "shop & ops", dimensions: { service: "checkout" }, bucket: { lower: 1, upper: 2 } };
  await getTrace(target, signal);
  const url = new URL(lastCall()[0], "http://localhost");
  expect(url.pathname).toBe("/api/observability/trace");
  expect(Object.fromEntries(url.searchParams)).toEqual({ trace_id: "a&b", namespace: "shop & ops", from: target.window_from, to: target.window_to, limit: "200" });
  expect(lastCall()[1].signal).toBe(signal);
  const body = { dashboard: spec, panel_id: target.panel_id, kind: "traces" as const, from: target.from, to: target.to, time: { from: target.window_from, to: target.window_to, refresh: "off" }, dimensions: target.dimensions, bucket: target.bucket };
  await queryExemplars(body, signal);
  expect(lastCall()[0]).toBe("/api/panels/exemplars");
  expect(lastCall()[1].method).toBe("POST");
  expect(lastCall()[1].signal).toBe(signal);
  expect(JSON.parse(String(lastCall()[1].body))).toEqual(body);
});
