import { describe, expect, it } from "vitest";
import { duration, percent } from "../../format";

describe("duration", () => {
  it("keeps sub-millisecond values legible", () => {
    expect(duration(0)).toBe("0ms");
    expect(duration(0.42)).toBe("0.42ms");
  });

  it("drops decimals as the millisecond value grows", () => {
    expect(duration(48.16)).toBe("48.2ms");
    expect(duration(240)).toBe("240ms");
  });

  it("reads seconds without false precision", () => {
    expect(duration(7650)).toBe("7.65s");
    expect(duration(13691)).toBe("13.7s");
  });

  // "600.00s" was two decimals of false precision on ten minutes, sitting in
  // the same column as "25.0ms".
  it("reads minutes and hours above a minute", () => {
    expect(duration(600_000)).toBe("10m");
    expect(duration(240_020)).toBe("4m");
    expect(duration(263_000)).toBe("4m 23s");
    expect(duration(3_600_000)).toBe("1h");
    expect(duration(5_400_000)).toBe("1h 30m");
  });

  it("never rounds seconds up into a full minute", () => {
    expect(duration(119_600)).toBe("2m");
  });

  it("says nothing rather than something wrong", () => {
    expect(duration(Number.NaN)).toBe("—");
  });
});

describe("percent", () => {
  it("keeps two decimals for small rates and one for large", () => {
    expect(percent(0.0216)).toBe("2.16%");
    expect(percent(0.141)).toBe("14.1%");
  });

  // A rate that rounds to zero beside a red badge reads as a contradiction.
  it("says a small rate is small rather than zero", () => {
    expect(percent(0.00002)).toBe("<0.01%");
    expect(percent(0)).toBe("0.00%");
  });
});
