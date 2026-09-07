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

describe("duration seams", () => {
  // 999.5ms used to print "1000ms": four digits of milliseconds sitting in a
  // column of seconds.
  it("never rounds up into the unit it just left", () => {
    expect(duration(999.5)).toBe("1.00s");
    expect(duration(999.9)).toBe("1.00s");
    expect(duration(99.96)).toBe("100ms");
    expect(duration(59_950)).toBe("1m");
    expect(duration(3_599_600)).toBe("1h");
  });

  // A microsecond span is not a zero-length one, for the same reason a rate of
  // 0.002% is not no errors.
  it("says a tiny duration is tiny rather than zero", () => {
    expect(duration(0.004)).toBe("<0.01ms");
    expect(duration(0)).toBe("0ms");
  });

  it("shows a negative duration rather than hiding it as missing", () => {
    expect(duration(-5)).toBe("-5.0ms");
  });
});
