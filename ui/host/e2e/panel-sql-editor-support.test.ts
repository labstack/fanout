import { beforeEach, expect, it, vi } from "vitest";
import type { APIRequestContext, Page, TestInfo } from "@playwright/test";
import { openSqlDashboard } from "./panel-sql-editor-support";

const { cleanupFailures, locator } = vi.hoisted(() => ({ cleanupFailures: [] as string[], locator: {} }));
vi.mock("@playwright/test", async () => {
  const { expect: assert } = await import("vitest");
  return { expect: Object.assign((value: unknown) => value === locator ? { toBeVisible: async () => {} } : assert(value), {
    // Playwright records a soft assertion failure without throwing. Model that
    // behavior here so a hard assertion would mask the test error and fail.
    soft: (value: unknown, message: string) => ({ toBe: (expected: unknown) => { if (value !== expected) cleanupFailures.push(message); } }),
  }) };
});
beforeEach(() => { cleanupFailures.length = 0; });

async function fixture(status = 204, reject = false) {
  const remove = vi.fn().mockImplementation(async () => {
    if (reject) throw new Error("Request failed");
    return { ok: () => status >= 200 && status < 300, status: () => status };
  });
  const request = { post: async () => ({ status: () => 201, json: async () => ({ id: "sql-dashboard", version: 1 }) }), delete: remove } as unknown as APIRequestContext;
  const page = { addInitScript: async () => {}, goto: async () => {}, locator: () => ({ locator: () => locator }) } as unknown as Page;
  const info = { project: { name: "test", use: {} }, testId: "test", retry: 0 } as TestInfo;
  return { ...await openSqlDashboard(page, request, info), remove };
}

it("confirms dashboard deletion with the dashboard id and asserts success", async () => {
  const { cleanup, remove } = await fixture();
  await cleanup();
  expect(remove).toHaveBeenCalledWith("/api/dashboards/sql-dashboard", { headers: { "Fanout-Request": "1", "Fanout-Confirm-Delete": "sql-dashboard" } });
  expect(cleanupFailures).toEqual([]);
});

it.each(["HTTP", "request"])("reports a %s cleanup failure even when the test passed", async failure => {
  const { cleanup } = await fixture(428, failure === "request");
  await cleanup();
  expect(cleanupFailures).toEqual([expect.stringContaining(failure === "HTTP" ? "HTTP 428" : "request failed")]);
});

it.each(["HTTP", "request"])("preserves the test's original error when %s cleanup fails", async failure => {
  const { cleanup } = await fixture(428, failure === "request");
  const original = new Error("Original test failure");
  await expect((async () => {
    try { throw original; }
    finally { await cleanup(); }
  })()).rejects.toBe(original);
  expect(cleanupFailures).toHaveLength(1);
});
