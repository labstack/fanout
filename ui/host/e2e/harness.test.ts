import { describe, expect, it } from "vitest";
import { inside, overlaps } from "./geometry";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import FailureReport from "./failure-report";

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
