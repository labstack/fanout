import { describe, expect, it } from "vitest";
import { healthSymbol } from "../../chart";
import { errorRateTone, latencyTone } from "../../health";

describe("status encoding", () => {
  // Health was drawn in hue alone, which is the one channel a reader with a
  // colour vision deficiency does not have.
  it("gives each health state a distinct shape", () => {
    const shapes = ["healthy", "degraded", "unhealthy"].map(healthSymbol);
    expect(new Set(shapes).size).toBe(3);
    expect(healthSymbol("unknown")).toBe(healthSymbol("healthy"));
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
