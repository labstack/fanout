import { describe, expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { chartLabel, diagnosticTail, installSignalCleanup, readinessCause, safeRequest, waitUntilReady } from "./setup-support";
import { inside, overlaps } from "./geometry";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import FailureReport from "./failure-report";

describe("setup safety and readiness", () => {
  it("redacts credentials before printing the last fifty diagnostic lines", () => {
    const lines = Array.from({ length: 60 }, (_, i) => `line ${i}`);
    lines.push('Bearer fo_ingest.abc-123 setup_token=secret%20token&next=/ cookie: fanout_session=session-secret; other=second-secret');
    lines.push('Set-Cookie: fanout_session=response-secret; Path=/; HttpOnly');
    const tail = diagnosticTail(lines.join("\n"));
    expect(tail.split("\n")).toHaveLength(50);
    expect(tail).toContain("line 59");
    for (const secret of ["fo_ingest", "secret%20token", "session-secret", "second-secret", "response-secret"]) expect(tail).not.toContain(secret);
    expect(tail).toContain("[REDACTED]");
    expect(diagnosticTail('{"cookie":"session=json-secret"}')).not.toContain("json-secret");
  });

  it("drops request call logs and reports only the path and status", async () => {
    await expect(safeRequest("/api/dashboards", async () => { throw new Error("cookie: session=private"); })).rejects.toThrow(/^Request \/api\/dashboards failed$/);
    await expect(safeRequest("/api/dashboards", async () => ({ ok: () => false, status: () => 503 }))).rejects.toThrow(/^HTTP 503 \/api\/dashboards$/);
  });

  it("retries startup and panel failures until readiness succeeds", async () => {
    const probe = vi.fn().mockRejectedValueOnce(new Error("HTTP 503 /readyz")).mockResolvedValueOnce("Panel map status error").mockResolvedValueOnce(undefined);
    await waitUntilReady(Date.now() + 500, probe, 1);
    expect(probe).toHaveBeenCalledTimes(3);
  });

  it("reports the last readiness cause at the deadline", async () => {
    await expect(waitUntilReady(Date.now() + 10, async () => "Panel map status error", 1)).rejects.toThrow("Panel map status error");
  });

  it("requires both signal counts, six nodes and at least one topology edge", () => {
    const counts = ["count", "log_count"].map(id => ({ id, status: "ok", frame: { rows: 1, columns: [{ name: "count" }], values: [[720]] } }));
    const map = (kinds: string[]) => ({ id: "map", status: "ok", frame: { rows: kinds.length, columns: [{ name: "kind" }], values: [kinds] } });
    expect(readinessCause({ results: [...counts, map(Array(6).fill("node"))] })).toContain("edge");
    expect(readinessCause({ results: [...counts, map([...Array(6).fill("node"), "edge"])] })).toBeUndefined();
    expect(readinessCause({ results: [...counts.slice(1), map([...Array(6).fill("node"), "edge"])] })).toContain("count");
    expect(readinessCause({ results: [{ id: "map", status: "error" }] })).toBe("Panel map status error");
  });

  it.each(["SIGINT", "SIGTERM"] as const)("cleans up before re-raising %s and unregisters handlers", signal => {
    const emitter = new EventEmitter();
    const calls: string[] = [];
    const remove = installSignalCleanup(() => calls.push("cleanup"), emitter, value => calls.push(value));
    emitter.emit(signal);
    expect(calls).toEqual(["cleanup", signal]);
    expect(emitter.listenerCount("SIGINT")).toBe(0);
    expect(emitter.listenerCount("SIGTERM")).toBe(0);
    remove();
  });
});

describe("chart identity", () => {
  it.each(["gauge", "timeseries", "bar", "heatmap", "histogram", "scatter", "state_timeline"])("requires the panel title and correct %s label", viz => {
    const label = chartLabel("Latency (p95)", viz)!;
    expect(label.test("Other panel: gauge")).toBe(false);
    expect(label.test("Latency (p95): unrelated chart: 1 series")).toBe(false);
  });
  it("accepts populated overview labels and rejects zero series", () => {
    expect(chartLabel("Requests", "timeseries", true)!.test("Requests: time series: 6 series; time range")).toBe(true);
    expect(chartLabel("Requests", "timeseries", true)!.test("Requests: time series: 0 series")).toBe(false);
  });
});

describe("smoke geometry", () => {
  const a = { x: 0, y: 0, width: 100, height: 100 };
  it("allows one pixel at shared edges but rejects intersections", () => {
    expect(overlaps(a, { ...a, x: 99 })).toBe(false);
    expect(overlaps(a, { ...a, x: 98 })).toBe(true);
  });
  it("allows one pixel outside a card but rejects clipping", () => {
    expect(inside({ ...a, x: -1 }, a)).toBe(true);
    expect(inside({ ...a, x: -2 }, a)).toBe(false);
  });
});

describe("failure HTML report", () => {
  it("writes escaped failures only after a failing run", async () => {
    const dir = await mkdtemp(join(tmpdir(), "fanout-report-test-"));
    try {
      const report = new FailureReport({ outputFolder: dir });
      await report.onEnd({ status: "passed", startTime: new Date(), duration: 0 });
      await expect(stat(join(dir, "index.html"))).rejects.toThrow();
      report.onError({ message: "<broken>" });
      await report.onEnd({ status: "failed", startTime: new Date(), duration: 0 });
      expect(await readFile(join(dir, "index.html"), "utf8")).toContain("&lt;broken&gt;");
    } finally { await rm(dir, { recursive: true, force: true }); }
  });
});
