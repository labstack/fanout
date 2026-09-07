import { describe, expect, it } from "vitest";
import { healthBorderType, healthColor, healthSymbol, healthSymbolScale } from "../../chart";
import { errorRateTone, latencyTone } from "../../health";

describe("status encoding", () => {
  // Health was drawn in hue alone, which is the one channel a reader with a
  // colour vision deficiency does not have.
  it("gives each health state a distinct shape", () => {
    const shapes = ["healthy", "degraded", "unhealthy"].map(healthSymbol);
    expect(new Set(shapes).size).toBe(3);
    // Shape is the severity channel and an ungraded node has no place in it,
    // so it keeps the circle and separates itself by outline and by colour.
    expect(healthSymbol("unknown")).toBe(healthSymbol("healthy"));
    expect(healthBorderType("unknown")).not.toBe(healthBorderType("healthy"));
    expect(healthColor("unknown")).not.toBe(healthColor("healthy"));
  });

  it("colours a figure by its own threshold, not the row verdict", () => {
    // Slow with no errors: the latency is the problem, and only the latency.
    expect(latencyTone(2500)).toBe("bad");
    expect(errorRateTone(0)).toBe("dimmed");
    // Fast but failing: the inverse.
    expect(latencyTone(12)).toBeUndefined();
    expect(errorRateTone(0.1)).toBe("bad");
    expect(errorRateTone(0.02)).toBe("warn");
  });
});

describe("map symbols", () => {
  // ECharts sizes by bounding box, so a diamond drew a third less area than a
  // circle at the same size — the unhealthy node was the smallest on the map.
  it("evens out the area each shape actually draws", () => {
    const area = (health: string, fill: number) => (healthSymbolScale(health) ** 2) * fill;
    const unhealthy = area("unhealthy", 0.5);
    const degraded = area("degraded", 0.95);
    const healthy = area("healthy", 0.785);
    expect(unhealthy).toBeGreaterThan(healthy * 0.9);
    expect(Math.abs(unhealthy - degraded) / degraded).toBeLessThan(0.05);
  });
});
