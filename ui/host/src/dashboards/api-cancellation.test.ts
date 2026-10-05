import { afterEach, describe, expect, it, vi } from "vitest";
import type { DashboardSpec } from "../../../panels/types";
import { queryPanels, resolveVariables } from "./api";

afterEach(() => vi.unstubAllGlobals());

describe("dashboard request cancellation", () => {
  it("passes the supplied abort signal through both APIs to fetch", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ results: [], options: {} })));
    vi.stubGlobal("fetch", fetchMock);
    const controller = new AbortController();
    const body = { dashboard: { version: 1, name: "test", time: {}, panels: [] } as DashboardSpec };
    await queryPanels(body, controller.signal);
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ options: {} })));
    await resolveVariables(body, controller.signal);
    expect(fetchMock.mock.calls.map((call) => call[1].signal)).toEqual([controller.signal, controller.signal]);
  });
});
